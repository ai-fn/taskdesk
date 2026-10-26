package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/fsociety/todo/pkg/events"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/twmb/franz-go/pkg/kgo"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Embedding the generated interfaces makes any method a test doesn't set up nil-panic.

// fakeEmbedder counts Voyage requests (calls) and embedded texts (texts).
type fakeEmbedder struct {
	mu    sync.Mutex
	calls int
	texts int
	err   error
}

func (f *fakeEmbedder) Embed(context.Context, string, string) ([]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.texts++
	if f.err != nil {
		return nil, f.err
	}
	return []float32{1}, nil
}

func (f *fakeEmbedder) EmbedBatch(_ context.Context, texts []string, _ string) ([][]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	f.texts += len(texts)
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1}
	}
	return out, nil
}

type fakeAuth struct {
	authv1.AuthServiceClient
	plans      map[string]string // user id -> plan
	getErr     error
	listFilter string
	pageSize   int // 0 = everything in one response
}

func (f *fakeAuth) GetUserByID(_ context.Context, in *authv1.GetUserByIDRequest, _ ...grpc.CallOption) (*authv1.GetUserByIDResponse, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &authv1.GetUserByIDResponse{User: &authv1.UserSummary{UserId: in.UserId, Plan: f.plans[in.UserId]}}, nil
}

func (f *fakeAuth) ListUserIDs(_ context.Context, in *authv1.ListUserIDsRequest, _ ...grpc.CallOption) (*authv1.ListUserIDsResponse, error) {
	f.listFilter = in.PlanFilter
	var users []*authv1.UserSummary
	for id, plan := range f.plans {
		if in.PlanFilter == "" || plan == in.PlanFilter {
			users = append(users, &authv1.UserSummary{UserId: id, Plan: plan})
		}
	}
	sort.Slice(users, func(i, j int) bool { return users[i].UserId < users[j].UserId })
	if f.pageSize == 0 {
		return &authv1.ListUserIDsResponse{Users: users}, nil
	}
	var page []*authv1.UserSummary // the token is the last id served
	for _, u := range users {
		if u.UserId > in.PageToken && len(page) < f.pageSize {
			page = append(page, u)
		}
	}
	resp := &authv1.ListUserIDsResponse{Users: page}
	if len(page) == f.pageSize && page[len(page)-1].UserId != users[len(users)-1].UserId {
		resp.NextPageToken = page[len(page)-1].UserId
	}
	return resp, nil
}

type fakeTodo struct {
	todov1.TodoServiceClient
	mu       sync.Mutex
	tasks    map[string][]string // user id -> task ids, sorted
	embedded map[string]bool
	getTasks int
	upserts  int
	getErr   error
}

func (f *fakeTodo) GetTask(_ context.Context, in *todov1.GetTaskRequest, _ ...grpc.CallOption) (*todov1.GetTaskResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getTasks++
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &todov1.GetTaskResponse{Task: &todov1.Task{Id: in.TaskId, Title: "title " + in.TaskId}}, nil
}

func (f *fakeTodo) UpsertTaskEmbedding(_ context.Context, in *todov1.UpsertTaskEmbeddingRequest, _ ...grpc.CallOption) (*todov1.UpsertTaskEmbeddingResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts++
	f.embedded[in.TaskId] = true
	return &todov1.UpsertTaskEmbeddingResponse{}, nil
}

// ListUnembeddedTasks mirrors the real RPC: id order, keyset page token, next token on a full page.
func (f *fakeTodo) ListUnembeddedTasks(_ context.Context, in *todov1.ListUnembeddedTasksRequest, _ ...grpc.CallOption) (*todov1.ListTasksResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	resp := &todov1.ListTasksResponse{}
	for _, id := range f.tasks[in.UserId] {
		if f.embedded[id] || id <= in.PageToken {
			continue
		}
		resp.Tasks = append(resp.Tasks, &todov1.Task{Id: id, Title: "title " + id})
		if len(resp.Tasks) == int(in.PageSize) {
			resp.NextPageToken = id
			break
		}
	}
	return resp, nil
}

func newHandler(emb *fakeEmbedder, auth *fakeAuth, todo *fakeTodo) *Handler {
	h := New(nil, todo, auth, slog.New(slog.NewTextHandler(io.Discard, nil)), 3)
	h.emb = emb
	h.limiter = rate.NewLimiter(rate.Inf, 1)
	return h
}

func taskRecord(t *testing.T, eventType, userID, taskID string) *kgo.Record {
	t.Helper()
	b, err := json.Marshal(events.TaskEvent{EventType: eventType, UserID: userID, TaskID: taskID})
	if err != nil {
		t.Fatal(err)
	}
	return &kgo.Record{Topic: events.Topic, Value: b}
}

func TestNew_LimiterFollowsConfiguredRPM(t *testing.T) {
	cases := []struct {
		rpm  int
		want time.Duration
	}{
		{3, 20 * time.Second},
		{60, time.Second},
		{0, 20 * time.Second}, // unset falls back to the free-tier default
		{-5, 20 * time.Second},
	}
	for _, tc := range cases {
		h := New(nil, nil, nil, slog.Default(), tc.rpm)
		if got, want := h.limiter.Limit(), rate.Every(tc.want); got != want {
			t.Errorf("rpm %d: limit = %v, want %v", tc.rpm, got, want)
		}
	}
}

func TestRebalanceTimeout_OutlastsABatchAtAnyRPM(t *testing.T) {
	for _, rpm := range []int{1, 3, 60, 600} {
		h := New(nil, nil, nil, slog.Default(), rpm)
		worstBatch := pollRecords * h.maxLiveWait
		if got := h.rebalanceTimeout(); got < minRebalanceWait || got <= worstBatch {
			t.Errorf("rpm %d: rebalance timeout %v must be >= %v and exceed a worst-case batch of %v", rpm, got, minRebalanceWait, worstBatch)
		}
	}
}

func TestHandle_FreeUserIsNotEmbedded(t *testing.T) {
	emb, todo := &fakeEmbedder{}, &fakeTodo{embedded: map[string]bool{}}
	h := newHandler(emb, &fakeAuth{plans: map[string]string{"u1": "free"}}, todo)

	if err := h.Handle(context.Background(), taskRecord(t, events.TypeTaskCreated, "u1", "t1")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if emb.calls != 0 || todo.getTasks != 0 || todo.upserts != 0 {
		t.Errorf("free user triggered work: embed=%d getTask=%d upsert=%d", emb.calls, todo.getTasks, todo.upserts)
	}
}

func TestHandle_ProUserIsEmbedded(t *testing.T) {
	emb, todo := &fakeEmbedder{}, &fakeTodo{embedded: map[string]bool{}}
	h := newHandler(emb, &fakeAuth{plans: map[string]string{"u1": "pro"}}, todo)

	if err := h.Handle(context.Background(), taskRecord(t, events.TypeTaskUpdated, "u1", "t1")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if emb.calls != 1 || !todo.embedded["t1"] {
		t.Errorf("embed=%d embedded=%v, want one embedding stored", emb.calls, todo.embedded)
	}
}

func TestHandle_PlanLookupErrorIsReturnedForRetry(t *testing.T) {
	emb, todo := &fakeEmbedder{}, &fakeTodo{embedded: map[string]bool{}}
	h := newHandler(emb, &fakeAuth{getErr: errors.New("auth down")}, todo)

	if err := h.Handle(context.Background(), taskRecord(t, events.TypeTaskCreated, "u1", "t1")); err == nil {
		t.Fatal("expected error so the record is retried, not dropped")
	}
	if emb.calls != 0 {
		t.Errorf("embedded %d times without knowing the plan", emb.calls)
	}
}

func TestHandle_DeletedUserIsSkippedNotRetried(t *testing.T) {
	emb, todo := &fakeEmbedder{}, &fakeTodo{embedded: map[string]bool{}}
	h := newHandler(emb, &fakeAuth{getErr: status.Error(codes.NotFound, "user not found")}, todo)

	if err := h.Handle(context.Background(), taskRecord(t, events.TypeTaskCreated, "gone", "t1")); err != nil {
		t.Errorf("Handle = %v, want nil: a deleted user must not be retried into the DLQ", err)
	}
	if emb.calls != 0 {
		t.Errorf("embedded %d times for a deleted user", emb.calls)
	}
}

func TestHandle_PurgedTaskIsSkippedNotRetried(t *testing.T) {
	emb := &fakeEmbedder{}
	todo := &fakeTodo{embedded: map[string]bool{}, getErr: status.Error(codes.NotFound, "task not found")}
	h := newHandler(emb, &fakeAuth{plans: map[string]string{"u1": "pro"}}, todo)

	if err := h.Handle(context.Background(), taskRecord(t, events.TypeTaskUpdated, "u1", "t1")); err != nil {
		t.Errorf("Handle = %v, want nil", err)
	}
	if emb.calls != 0 {
		t.Errorf("embedded %d times for a purged task", emb.calls)
	}
}

// With a slow limit the live path must fail fast instead of holding the Kafka batch.
func TestHandle_LimiterWaitIsBounded(t *testing.T) {
	emb, todo := &fakeEmbedder{}, &fakeTodo{embedded: map[string]bool{}}
	h := newHandler(emb, &fakeAuth{plans: map[string]string{"u1": "pro"}}, todo)
	h.limiter = rate.NewLimiter(rate.Every(time.Hour), 1)
	h.limiter.Allow() // spend the burst token
	h.maxLiveWait = 50 * time.Millisecond

	start := time.Now()
	err := h.Handle(context.Background(), taskRecord(t, events.TypeTaskCreated, "u1", "t1"))
	if err == nil {
		t.Fatal("expected a rate-limit error so the record is retried")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Handle blocked for %v, want it to fail fast", elapsed)
	}
	if emb.calls != 0 {
		t.Error("embedded despite the limiter refusing")
	}
}

func TestBackfill_ProUsersAndOnlyMissingEmbeddings(t *testing.T) {
	emb := &fakeEmbedder{}
	todo := &fakeTodo{
		tasks: map[string][]string{
			"pro1":  {"a1", "a2", "a3", "a4", "a5"},
			"free1": {"f1", "f2"},
		},
		embedded: map[string]bool{"a2": true, "a4": true},
	}
	auth := &fakeAuth{plans: map[string]string{"pro1": "pro", "free1": "free"}}
	h := newHandler(emb, auth, todo)

	h.Backfill(context.Background())

	if auth.listFilter != "pro" {
		t.Errorf("plan filter = %q, want pro", auth.listFilter)
	}
	if emb.calls != 1 || emb.texts != 3 {
		t.Errorf("voyage requests=%d texts=%d, want 1 request embedding a1, a3, a5", emb.calls, emb.texts)
	}
	for _, id := range []string{"f1", "f2"} {
		if todo.embedded[id] {
			t.Errorf("free user's task %s was embedded", id)
		}
	}
}

func TestBackfill_PagesThroughAllMissing(t *testing.T) {
	emb := &fakeEmbedder{}
	var ids []string
	for i := 0; i < 120; i++ { // > 2 pages of 50
		ids = append(ids, fmt.Sprintf("t%03d", i))
	}
	todo := &fakeTodo{tasks: map[string][]string{"u": ids}, embedded: map[string]bool{}}
	h := newHandler(emb, &fakeAuth{plans: map[string]string{"u": "pro"}}, todo)

	h.Backfill(context.Background())

	if emb.calls != 3 || emb.texts != 120 {
		t.Errorf("voyage requests=%d texts=%d, want 3 requests (50+50+20) for 120 tasks", emb.calls, emb.texts)
	}
}

func TestBackfill_SecondRunMakesNoVoyageCalls(t *testing.T) {
	emb := &fakeEmbedder{}
	todo := &fakeTodo{tasks: map[string][]string{"u": {"a", "b", "c"}}, embedded: map[string]bool{}}
	h := newHandler(emb, &fakeAuth{plans: map[string]string{"u": "pro"}}, todo)

	h.Backfill(context.Background())
	first := emb.calls
	h.Backfill(context.Background())

	if first != 1 || emb.calls != first {
		t.Errorf("first run=%d total=%d, want 1 request then no new calls on restart", first, emb.calls)
	}
}

func TestBackfill_EmbedFailureDoesNotLoopOrStop(t *testing.T) {
	emb := &fakeEmbedder{err: errors.New("voyage 429")}
	var ids []string
	for i := 0; i < 120; i++ { // several pages, so a stuck cursor would show
		ids = append(ids, fmt.Sprintf("t%03d", i))
	}
	todo := &fakeTodo{tasks: map[string][]string{"u": ids}, embedded: map[string]bool{}}
	h := newHandler(emb, &fakeAuth{plans: map[string]string{"u": "pro"}}, todo)

	done := make(chan struct{})
	go func() { h.Backfill(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Backfill did not terminate when every embed fails")
	}
	if emb.calls != 3 {
		t.Errorf("voyage requests = %d, want 3 (each page tried once)", emb.calls)
	}
}

func TestBackfill_StopsQuietlyWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	emb := &fakeEmbedder{err: context.Canceled}
	todo := &fakeTodo{tasks: map[string][]string{"a": {"1", "2"}, "b": {"3"}, "c": {"4"}}, embedded: map[string]bool{}}
	h := newHandler(emb, &fakeAuth{plans: map[string]string{"a": "pro", "b": "pro", "c": "pro"}}, todo)
	cancel()

	h.Backfill(ctx)

	if emb.calls > 1 || todo.upserts != 0 {
		t.Errorf("voyage requests=%d upserts=%d after cancel, want at most the first request and no work for later users", emb.calls, todo.upserts)
	}
}

func TestBackfill_WalksEveryPageOfUsers(t *testing.T) {
	emb := &fakeEmbedder{}
	plans, tasks := map[string]string{}, map[string][]string{}
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("u%d", i)
		plans[id], tasks[id] = "pro", []string{"t-" + id}
	}
	todo := &fakeTodo{tasks: tasks, embedded: map[string]bool{}}
	h := newHandler(emb, &fakeAuth{plans: plans, pageSize: 3}, todo)

	h.Backfill(context.Background())

	if emb.texts != 7 {
		t.Errorf("embedded %d tasks, want 7: users beyond the first page of 3 were skipped", emb.texts)
	}
}
