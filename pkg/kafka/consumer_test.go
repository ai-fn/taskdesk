package kafka

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

type fakeClient struct {
	// polledMax records the maxPollRecords the consumer asked for.
	polledMax int
	mu        sync.Mutex
	fetches   []kgo.Fetches
	cancel    context.CancelFunc
	commits   [][]*kgo.Record
	produced  []*kgo.Record
	// produceErrs are returned by successive ProduceSync calls before it succeeds.
	produceErrs []error
	// cancelAfterProduceErrs cancels once the last scripted produce error is returned.
	cancelAfterProduceErrs bool
	commitErr              error
	// commitCtxErrs records ctx.Err() seen by each CommitRecords call.
	commitCtxErrs []error
	rebalances    int
	pingErr       error
}

func (f *fakeClient) PollRecords(_ context.Context, n int) kgo.Fetches {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polledMax = n
	if len(f.fetches) == 0 {
		f.cancel()
		return nil
	}
	next := f.fetches[0]
	f.fetches = f.fetches[1:]
	return next
}

func (f *fakeClient) AllowRebalance() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rebalances++
}

func (f *fakeClient) CommitRecords(ctx context.Context, rs ...*kgo.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commitCtxErrs = append(f.commitCtxErrs, ctx.Err())
	f.commits = append(f.commits, rs)
	return f.commitErr
}

func (f *fakeClient) ProduceSync(_ context.Context, rs ...*kgo.Record) kgo.ProduceResults {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.produceErrs) > 0 {
		err := f.produceErrs[0]
		f.produceErrs = f.produceErrs[1:]
		if len(f.produceErrs) == 0 && f.cancelAfterProduceErrs {
			f.cancel()
		}
		return kgo.ProduceResults{{Err: err}}
	}
	f.produced = append(f.produced, rs...)
	return kgo.ProduceResults{{}}
}

func (f *fakeClient) Ping(context.Context) error { return f.pingErr }

func (f *fakeClient) Close() {}

func rec(topic string, partition int32, offset int64) *kgo.Record {
	return &kgo.Record{Topic: topic, Partition: partition, Offset: offset, Key: []byte("k"), Value: []byte("v")}
}

func fetchOf(recs ...*kgo.Record) kgo.Fetches {
	byTP := map[topicPartition][]*kgo.Record{}
	var order []topicPartition
	for _, r := range recs {
		tp := topicPartition{r.Topic, r.Partition}
		if _, ok := byTP[tp]; !ok {
			order = append(order, tp)
		}
		byTP[tp] = append(byTP[tp], r)
	}
	var topics []kgo.FetchTopic
	for _, tp := range order {
		topics = append(topics, kgo.FetchTopic{
			Topic:      tp.topic,
			Partitions: []kgo.FetchPartition{{Partition: tp.partition, Records: byTP[tp]}},
		})
	}
	return kgo.Fetches{{Topics: topics}}
}

func newTestConsumer(t *testing.T, fc *fakeClient) (*Consumer, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	ctx, cancel2 := context.WithCancel(ctx)
	fc.cancel = cancel2
	c := newConsumer(fc, "grp", slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.baseBackoff = time.Microsecond
	return c, ctx
}

func committedOffsets(fc *fakeClient) map[topicPartition]int64 {
	got := map[topicPartition]int64{}
	for _, batch := range fc.commits {
		for _, r := range batch {
			got[topicPartition{r.Topic, r.Partition}] = r.Offset
		}
	}
	return got
}

func TestRun_CommitsLastRecordPerPartitionOncePerBatch(t *testing.T) {
	fc := &fakeClient{fetches: []kgo.Fetches{
		fetchOf(rec("t", 0, 1), rec("t", 0, 2), rec("t", 1, 7)),
	}}
	c, ctx := newTestConsumer(t, fc)

	var handled []int64
	err := c.Run(ctx, func(_ context.Context, r *kgo.Record) error {
		handled = append(handled, r.Offset)
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !reflect.DeepEqual(handled, []int64{1, 2, 7}) {
		t.Errorf("handled = %v", handled)
	}
	if len(fc.commits) != 1 {
		t.Fatalf("commit RPCs = %d, want 1", len(fc.commits))
	}
	want := map[topicPartition]int64{{"t", 0}: 2, {"t", 1}: 7}
	if got := committedOffsets(fc); !reflect.DeepEqual(got, want) {
		t.Errorf("committed = %v, want %v", got, want)
	}
}

func TestRun_RetriesFailedRecordBeforeCommitting(t *testing.T) {
	fc := &fakeClient{fetches: []kgo.Fetches{fetchOf(rec("t", 0, 1), rec("t", 0, 2))}}
	c, ctx := newTestConsumer(t, fc)

	calls := map[int64]int{}
	err := c.Run(ctx, func(_ context.Context, r *kgo.Record) error {
		calls[r.Offset]++
		if r.Offset == 1 && calls[1] < 3 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls[1] != 3 || calls[2] != 1 {
		t.Errorf("calls = %v, want offset1=3 offset2=1", calls)
	}
	if len(fc.produced) != 0 {
		t.Errorf("unexpected DLQ records: %d", len(fc.produced))
	}
	if got := committedOffsets(fc)[topicPartition{"t", 0}]; got != 2 {
		t.Errorf("committed offset = %d, want 2", got)
	}
}

func TestRun_DeadLettersPermanentFailureThenAdvances(t *testing.T) {
	fc := &fakeClient{fetches: []kgo.Fetches{fetchOf(rec("t", 0, 1), rec("t", 0, 2))}}
	c, ctx := newTestConsumer(t, fc)
	c.maxAttempts = 3

	calls := map[int64]int{}
	err := c.Run(ctx, func(_ context.Context, r *kgo.Record) error {
		calls[r.Offset]++
		if r.Offset == 1 {
			return errors.New("poison")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls[1] != 3 {
		t.Errorf("poison record attempts = %d, want 3", calls[1])
	}
	if calls[2] != 1 {
		t.Errorf("record behind poison handled %d times, want 1", calls[2])
	}
	if len(fc.produced) != 1 {
		t.Fatalf("DLQ records = %d, want 1", len(fc.produced))
	}
	d := fc.produced[0]
	if d.Topic != "t"+DLQSuffix || string(d.Key) != "k" || string(d.Value) != "v" {
		t.Errorf("DLQ record = topic %q key %q value %q", d.Topic, d.Key, d.Value)
	}
	hdr := map[string]string{}
	for _, h := range d.Headers {
		hdr[h.Key] = string(h.Value)
	}
	want := map[string]string{"dlq-source-topic": "t", "dlq-source-partition": "0", "dlq-source-offset": "1", "dlq-consumer-group": "grp", "dlq-error": "poison"}
	if !reflect.DeepEqual(hdr, want) {
		t.Errorf("DLQ headers = %v, want %v", hdr, want)
	}
	if got := committedOffsets(fc)[topicPartition{"t", 0}]; got != 2 {
		t.Errorf("committed offset = %d, want 2", got)
	}
}

func TestRun_DLQProduceFailureIsRetriedNotSkipped(t *testing.T) {
	fc := &fakeClient{
		fetches:     []kgo.Fetches{fetchOf(rec("t", 0, 1))},
		produceErrs: []error{errors.New("broker down"), errors.New("broker down")},
	}
	c, ctx := newTestConsumer(t, fc)
	c.maxAttempts = 1

	err := c.Run(ctx, func(context.Context, *kgo.Record) error { return errors.New("poison") })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fc.produced) != 1 {
		t.Errorf("DLQ records = %d, want 1 after retries", len(fc.produced))
	}
	if got := committedOffsets(fc)[topicPartition{"t", 0}]; got != 1 {
		t.Errorf("committed offset = %d, want 1", got)
	}
}

func TestRun_NeverCommitsPastUnhandledRecordOnShutdown(t *testing.T) {
	fc := &fakeClient{fetches: []kgo.Fetches{fetchOf(rec("t", 0, 1), rec("t", 0, 2), rec("t", 0, 3))}}
	c, ctx := newTestConsumer(t, fc)

	err := c.Run(ctx, func(_ context.Context, r *kgo.Record) error {
		if r.Offset == 2 {
			fc.cancel() // shutdown arrives while record 2 is failing
			return errors.New("interrupted")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := committedOffsets(fc)[topicPartition{"t", 0}]; got != 1 {
		t.Errorf("committed offset = %d, want 1 (record 2 unhandled)", got)
	}
	if len(fc.produced) != 0 {
		t.Errorf("shutdown must not dead-letter, got %d DLQ records", len(fc.produced))
	}
	for _, e := range fc.commitCtxErrs {
		if e != nil {
			t.Errorf("shutdown commit ran with a cancelled ctx: %v", e)
		}
	}
}

func TestRun_StopsHandlingBatchAfterShutdown(t *testing.T) {
	fc := &fakeClient{fetches: []kgo.Fetches{fetchOf(rec("t", 0, 1), rec("t", 0, 2))}}
	c, ctx := newTestConsumer(t, fc)

	var handled []int64
	err := c.Run(ctx, func(_ context.Context, r *kgo.Record) error {
		handled = append(handled, r.Offset)
		fc.cancel() // shutdown mid-batch; the handler still succeeds
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !reflect.DeepEqual(handled, []int64{1}) {
		t.Errorf("handled = %v, want [1]: record 2 must not run after shutdown", handled)
	}
	if got := committedOffsets(fc)[topicPartition{"t", 0}]; got != 1 {
		t.Errorf("committed offset = %d, want 1", got)
	}
}

func TestRun_ShutdownDuringDLQProduceDoesNotCommit(t *testing.T) {
	fc := &fakeClient{
		fetches:     []kgo.Fetches{fetchOf(rec("t", 0, 1))},
		produceErrs: []error{errors.New("broker down")},
	}
	c, ctx := newTestConsumer(t, fc)
	c.maxAttempts = 1
	fc.cancelAfterProduceErrs = true

	err := c.Run(ctx, func(context.Context, *kgo.Record) error { return errors.New("poison") })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fc.commits) != 0 || len(fc.produced) != 0 {
		t.Errorf("commits=%d produced=%d, want none", len(fc.commits), len(fc.produced))
	}
}

func TestRun_FailingPartitionDoesNotHoldBackHandledPartition(t *testing.T) {
	fc := &fakeClient{fetches: []kgo.Fetches{fetchOf(rec("t", 0, 5), rec("t", 1, 9))}}
	c, ctx := newTestConsumer(t, fc)
	c.maxAttempts = 1

	err := c.Run(ctx, func(_ context.Context, r *kgo.Record) error {
		if r.Partition == 1 {
			return errors.New("poison")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := map[topicPartition]int64{{"t", 0}: 5, {"t", 1}: 9}
	if got := committedOffsets(fc); !reflect.DeepEqual(got, want) {
		t.Errorf("committed = %v, want %v", got, want)
	}
	if len(fc.produced) != 1 || fc.produced[0].Topic != "t"+DLQSuffix {
		t.Errorf("DLQ records = %v", fc.produced)
	}
}

func TestRun_AllowsRebalanceAfterEveryBatch(t *testing.T) {
	fc := &fakeClient{fetches: []kgo.Fetches{fetchOf(rec("t", 0, 1)), fetchOf(rec("t", 0, 2))}}
	c, ctx := newTestConsumer(t, fc)

	if err := c.Run(ctx, func(context.Context, *kgo.Record) error { return nil }); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fc.rebalances != 3 { // two batches + the empty poll that triggers shutdown
		t.Errorf("AllowRebalance calls = %d, want 3", fc.rebalances)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("abcdef", 3); got != "abc" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate("ab", 3); got != "ab" {
		t.Errorf("truncate = %q", got)
	}
}

func TestRun_HandlesRecordsAlongsidePartitionError(t *testing.T) {
	f := fetchOf(rec("t", 0, 1))
	f[0].Topics[0].Partitions = append(f[0].Topics[0].Partitions, kgo.FetchPartition{Partition: 1, Err: errors.New("fetch failed")})
	fc := &fakeClient{fetches: []kgo.Fetches{f}}
	c, ctx := newTestConsumer(t, fc)

	var handled int
	err := c.Run(ctx, func(context.Context, *kgo.Record) error { handled++; return nil })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if handled != 1 {
		t.Errorf("handled = %d, want 1: records next to a fetch error must not be dropped", handled)
	}
	if got := committedOffsets(fc)[topicPartition{"t", 0}]; got != 1 {
		t.Errorf("committed offset = %d, want 1", got)
	}
}

func TestBackoff_CappedAndOverflowSafe(t *testing.T) {
	c := newConsumer(&fakeClient{}, "grp", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if got := c.backoff(1); got != defaultBaseBackoff {
		t.Errorf("backoff(1) = %v", got)
	}
	for _, attempt := range []int{10, 70, 200} {
		if got := c.backoff(attempt); got != maxBackoff {
			t.Errorf("backoff(%d) = %v, want %v", attempt, got, maxBackoff)
		}
	}
}

func TestRun_PollsConfiguredBatchSize(t *testing.T) {
	fc := &fakeClient{}
	c, ctx := newTestConsumer(t, fc)
	if c.maxPoll != maxPollRecords {
		t.Fatalf("default maxPoll = %d, want %d", c.maxPoll, maxPollRecords)
	}
	c.maxPoll = 3
	if err := c.Run(ctx, func(context.Context, *kgo.Record) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if fc.polledMax != 3 {
		t.Errorf("PollRecords asked for %d, want 3", fc.polledMax)
	}
}

func TestConsumerPing(t *testing.T) {
	if err := newConsumer(&fakeClient{}, "g", slog.Default()).Ping(context.Background()); err != nil {
		t.Errorf("reachable broker: %v, want nil", err)
	}
	boom := errors.New("no brokers")
	if err := newConsumer(&fakeClient{pingErr: boom}, "g", slog.Default()).Ping(context.Background()); !errors.Is(err, boom) {
		t.Errorf("Ping = %v, want the client's error", err)
	}
}
