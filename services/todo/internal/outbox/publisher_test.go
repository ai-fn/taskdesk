package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	pkgkafka "github.com/fsociety/todo/pkg/kafka"
	"github.com/fsociety/todo/services/todo/internal/store"
	"github.com/fsociety/todo/services/todo/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	pool, teardown := testutil.StartPostgres()
	testPool = pool
	code := m.Run()
	teardown()
	os.Exit(code)
}

// fakeProducer records produced event ids and can fail chosen ones.
type fakeProducer struct {
	mu       sync.Mutex
	got      map[string]int
	failID   string
	failAll  bool
	delay    time.Duration
	calls    int
	deadline time.Time
	onCall   func()
}

func (f *fakeProducer) PublishBatch(ctx context.Context, _ string, msgs []pkgkafka.Message) []error {
	time.Sleep(f.delay) // widen the window in which a second publisher could race
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.deadline, _ = ctx.Deadline()
	if f.onCall != nil {
		f.onCall()
	}
	if f.got == nil {
		f.got = map[string]int{}
	}
	errs := make([]error, len(msgs))
	for i, m := range msgs {
		id := eventID(m.Value)
		if f.failAll || id == f.failID {
			errs[i] = errors.New("broker down")
			continue
		}
		f.got[id]++
	}
	return errs
}

func eventID(payload []byte) string {
	var ev struct {
		EventID string `json:"event_id"`
	}
	_ = json.Unmarshal(payload, &ev)
	return ev.EventID
}

func setup(t *testing.T) *store.Store {
	t.Helper()
	if testPool == nil {
		t.Skip("Docker unavailable")
	}
	testutil.Truncate(t, testPool)
	return store.New(testPool)
}

func newPublisher(st *store.Store, p producer) *Publisher {
	return &Publisher{store: st, producer: p, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func seedOutbox(t *testing.T, n int) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO todo.outbox (event_type, payload, created_at)
		SELECT 'task.created', jsonb_build_object('event_id', 'e' || g, 'user_id', 'u' || (g % 7)), now() + g * interval '1 millisecond'
		FROM generate_series(1, $1) g`, n); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, where string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM todo.outbox WHERE `+where).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTwoPublishersPublishEachRowOnce(t *testing.T) {
	st := setup(t)
	seedOutbox(t, 450)
	prod := &fakeProducer{delay: 5 * time.Millisecond}
	a, b := newPublisher(st, prod), newPublisher(st, prod)

	var wg sync.WaitGroup
	for _, p := range []*Publisher{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				full, err := p.flush(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				if !full {
					return
				}
			}
		}()
	}
	wg.Wait()

	if len(prod.got) != 450 {
		t.Fatalf("distinct events published = %d, want 450", len(prod.got))
	}
	for id, n := range prod.got {
		if n != 1 {
			t.Errorf("event %s published %d times, want once", id, n)
		}
	}
	if n := count(t, "published_at IS NULL"); n != 0 {
		t.Errorf("%d rows still unpublished", n)
	}
}

func TestPoisonRowDoesNotStallTheQueue(t *testing.T) {
	st := setup(t)
	ctx := context.Background()
	// The poison row is oldest and fills the head of the queue.
	if _, err := testPool.Exec(ctx, `INSERT INTO todo.outbox (event_type, payload, created_at) VALUES ('task.created', '{"event_id":"bad","user_id":123}', now() - interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	seedOutbox(t, 5)
	prod := &fakeProducer{}

	if _, err := newPublisher(st, prod).flush(ctx); err != nil {
		t.Fatal(err)
	}
	if len(prod.got) != 5 {
		t.Errorf("published = %d, want the 5 rows behind the poison row", len(prod.got))
	}
	var reason string
	if err := testPool.QueryRow(ctx, `SELECT last_error FROM todo.outbox WHERE failed_at IS NOT NULL`).Scan(&reason); err != nil || reason == "" {
		t.Errorf("poison row not parked with a reason: err=%v reason=%q", err, reason)
	}
	// A second pass must not touch the parked row again.
	if _, err := newPublisher(st, prod).flush(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, "failed_at IS NOT NULL"); n != 1 {
		t.Errorf("parked rows = %d, want 1", n)
	}
}

func TestFailedProduceIsRetriedAfterLeaseExpires(t *testing.T) {
	st := setup(t)
	ctx := context.Background()
	seedOutbox(t, 3)
	prod := &fakeProducer{failID: "e2"}
	p := newPublisher(st, prod)

	if _, err := p.flush(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, "published_at IS NULL"); n != 1 {
		t.Fatalf("unpublished = %d, want only the rejected row", n)
	}
	// Still leased: not re-sent in a tight loop.
	if _, err := p.flush(ctx); err != nil {
		t.Fatal(err)
	}
	if prod.got["e1"] != 1 || prod.got["e3"] != 1 || len(prod.got) != 2 {
		t.Fatalf("got = %v, want e1 and e3 once", prod.got)
	}

	if _, err := testPool.Exec(ctx, `UPDATE todo.outbox SET claimed_until = now() - interval '1 second' WHERE published_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	prod.failID = ""
	if _, err := p.flush(ctx); err != nil {
		t.Fatal(err)
	}
	if prod.got["e2"] != 1 || count(t, "published_at IS NULL") != 0 {
		t.Errorf("after lease expiry got = %v, want e2 published", prod.got)
	}
}

func TestFlushReportsFullBatch(t *testing.T) {
	st := setup(t)
	seedOutbox(t, batchSize+1)
	p := newPublisher(st, &fakeProducer{})
	if full, err := p.flush(context.Background()); err != nil || !full {
		t.Fatalf("first flush full=%v err=%v, want full", full, err)
	}
	if full, err := p.flush(context.Background()); err != nil || full {
		t.Fatalf("second flush full=%v err=%v, want not full", full, err)
	}
}

func TestPruneKeepsRecentAndUnpublishedRows(t *testing.T) {
	st := setup(t)
	ctx := context.Background()
	seedOutbox(t, 3)
	if _, err := testPool.Exec(ctx, `
		UPDATE todo.outbox SET published_at = now() - interval '8 days' WHERE payload->>'event_id' = 'e1';
		UPDATE todo.outbox SET published_at = now() - interval '1 day'  WHERE payload->>'event_id' = 'e2'`); err != nil {
		t.Fatal(err)
	}
	newPublisher(st, &fakeProducer{}).prune(ctx)
	if n := count(t, "true"); n != 2 {
		t.Errorf("rows after prune = %d, want 2 (old published row removed)", n)
	}
	if n := count(t, "payload->>'event_id' = 'e1'"); n != 0 {
		t.Error("the 8-day-old published row survived the prune")
	}
}

func TestRunDoesNotSpinThroughBacklogWhenKafkaRejects(t *testing.T) {
	st := setup(t)
	seedOutbox(t, 5*batchSize)
	prod := &fakeProducer{failAll: true}
	ctx, cancel := context.WithTimeout(context.Background(), 1300*time.Millisecond)
	defer cancel()

	newPublisher(st, prod).Run(ctx)

	prod.mu.Lock()
	defer prod.mu.Unlock()
	if prod.calls > 4 { // one per poll interval, not one per loop turn
		t.Errorf("PublishBatch called %d times in 1.3s, want at most one per poll interval", prod.calls)
	}
}

func TestProduceIsBoundedWellUnderTheLease(t *testing.T) {
	st := setup(t)
	seedOutbox(t, 1)
	prod := &fakeProducer{}
	if _, err := newPublisher(st, prod).flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if prod.deadline.IsZero() || time.Until(prod.deadline) > claimLease/2+time.Second {
		t.Errorf("produce deadline = %v from now, want <= claimLease/2", time.Until(prod.deadline))
	}
}

func TestShutdownReleasesLeasesOfUnsentRows(t *testing.T) {
	st := setup(t)
	seedOutbox(t, 3)
	ctx, cancel := context.WithCancel(context.Background())
	prod := &fakeProducer{failAll: true, onCall: cancel} // shutdown arrives mid-produce

	if _, err := newPublisher(st, prod).flush(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, "published_at IS NULL AND claimed_until IS NOT NULL"); n != 0 {
		t.Errorf("%d unsent rows still leased after shutdown, want 0", n)
	}
}

func TestPublishedRowsDropTheirLease(t *testing.T) {
	st := setup(t)
	seedOutbox(t, 2)
	if _, err := newPublisher(st, &fakeProducer{}).flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, "published_at IS NOT NULL AND claimed_until IS NOT NULL"); n != 0 {
		t.Errorf("%d published rows keep a lease", n)
	}
}

func TestClaimOutboxReturnsOldestFirst(t *testing.T) {
	st := setup(t)
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO todo.outbox (event_type, payload, created_at)
		SELECT 'task.created', jsonb_build_object('event_id', 'e' || g), now() - g * interval '1 minute'
		FROM generate_series(1, 5) g`); err != nil {
		t.Fatal(err)
	}
	entries, err := st.ClaimOutbox(ctx, 3, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("claimed %d, want 3", len(entries))
	}
	for i, want := range []string{"e5", "e4", "e3"} { // the oldest rows are the highest g
		if got := eventID(entries[i].Payload); got != want {
			t.Errorf("entries[%d] = %s, want %s", i, got, want)
		}
	}
}
