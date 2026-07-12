package taskprocessor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	commonerrors "github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/utils/backoff"
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/config"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shardmanager"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Fakes
//
// Minimal stand-ins for the three stores + ShardManager that the
// OpsBatchReader interacts with. Each fake records arguments for
// assertions and exposes hooks to inject failures so tests can exercise
// the indefinite-retry-no-skip semantics without spinning up a real Mongo.
// ============================================================================

// fakeOpsRunStore implements just the RunStore methods OpsBatchReader uses.
// All other RunStore methods panic if called — they should never be invoked
// from the OpsFIFO path.
type fakeOpsRunStore struct {
	mu    sync.Mutex
	tasks []*p.OpsFIFOTaskRow // queue, drained by RangeReadOpsFIFOTasks(afterSeq)
}

func (s *fakeOpsRunStore) push(t *p.OpsFIFOTaskRow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = append(s.tasks, t)
}

func (s *fakeOpsRunStore) RangeReadOpsFIFOTasks(_ context.Context, _ int32, afterSeq int64, limit int) ([]*p.OpsFIFOTaskRow, commonerrors.CategorizedError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*p.OpsFIFOTaskRow
	for _, t := range s.tasks {
		if t.SortKey > afterSeq {
			out = append(out, t)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (s *fakeOpsRunStore) RangeDeleteOpsFIFOTasks(_ context.Context, _ int32, _ int64) commonerrors.CategorizedError {
	return nil
}
func (s *fakeOpsRunStore) DeleteOpsFIFOTasksByIDBatch(_ context.Context, _ int32, _ []ids.TaskID) commonerrors.CategorizedError {
	return nil
}

// Stub-out the rest of RunStore so the type satisfies the interface for
// situations where the broader RunStore is needed (compile-time only —
// these are never invoked by OpsBatchReader).
func (s *fakeOpsRunStore) CreateRunWithTasks(context.Context, *p.RunRow, []p.TaskRow) commonerrors.CategorizedError {
	panic("CreateRunWithTasks not used by OpsBatchReader test")
}
func (s *fakeOpsRunStore) GetRun(context.Context, int32, string, string, p.GetRunOptions) (*p.RunRow, commonerrors.CategorizedError) {
	panic("GetRun not used by OpsBatchReader test")
}
func (s *fakeOpsRunStore) UpdateRunWithNewTasks(context.Context, int32, string, string, int64, *p.RunRowUpdate, []p.TaskRow) commonerrors.CategorizedError {
	panic("UpdateRunWithNewTasks not used by OpsBatchReader test")
}
func (s *fakeOpsRunStore) RangeReadImmediateTasks(context.Context, int32, int64, int) ([]*p.ImmediateTaskRow, commonerrors.CategorizedError) {
	panic("RangeReadImmediateTasks not used by OpsBatchReader test")
}
func (s *fakeOpsRunStore) RangeReadTimerTasks(context.Context, int32, int64, int64, ids.TaskID, int) ([]*p.TimerTaskRow, commonerrors.CategorizedError) {
	panic("RangeReadTimerTasks not used by OpsBatchReader test")
}
func (s *fakeOpsRunStore) RangeDeleteImmediateTasks(context.Context, int32, int64) commonerrors.CategorizedError {
	panic("RangeDeleteImmediateTasks not used by OpsBatchReader test")
}
func (s *fakeOpsRunStore) RangeDeleteTimerTasks(context.Context, int32, int64, ids.TaskID) commonerrors.CategorizedError {
	panic("RangeDeleteTimerTasks not used by OpsBatchReader test")
}
func (s *fakeOpsRunStore) DeleteImmediateTasksByIDBatch(context.Context, int32, []ids.TaskID) commonerrors.CategorizedError {
	panic("DeleteImmediateTasksByIDBatch not used by OpsBatchReader test")
}
func (s *fakeOpsRunStore) DeleteTimerTasksByIDBatch(context.Context, int32, []ids.TaskID) commonerrors.CategorizedError {
	panic("DeleteTimerTasksByIDBatch not used by OpsBatchReader test")
}
func (s *fakeOpsRunStore) DeleteAll(context.Context) error { return nil }
func (s *fakeOpsRunStore) Close() error                    { return nil }

// fakeHistoryStore records BatchInsertHistory calls and lets the test inject
// transient failures for the first N calls.
type fakeHistoryStore struct {
	mu          sync.Mutex
	batches     [][]p.HistoryEvent
	failNTimes  atomic.Int32
	failedCount atomic.Int32
}

func (s *fakeHistoryStore) BatchInsertHistory(_ context.Context, events []p.HistoryEvent) commonerrors.CategorizedError {
	if remaining := s.failNTimes.Add(-1); remaining >= 0 {
		s.failedCount.Add(1)
		return commonerrors.NewUnavailableError("fake transient", errors.New("nope"))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := make([]p.HistoryEvent, len(events))
	copy(clone, events)
	s.batches = append(s.batches, clone)
	return nil
}
func (s *fakeHistoryStore) GetHistoryEvents(context.Context, string, string, int64, int) ([]p.HistoryEvent, commonerrors.CategorizedError) {
	return nil, nil
}
func (s *fakeHistoryStore) GetLatestEvent(context.Context, string, string) (*p.HistoryEvent, commonerrors.CategorizedError) {
	return nil, nil
}
func (s *fakeHistoryStore) DeleteAll(context.Context) error { return nil }
func (s *fakeHistoryStore) Close() error                    { return nil }

func (s *fakeHistoryStore) batchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.batches)
}

// fakeVisibilityStore records BatchUpsertVisibility calls.
type fakeVisibilityStore struct {
	mu      sync.Mutex
	batches [][]p.VisibilityEntry
}

func (s *fakeVisibilityStore) BatchUpsertVisibility(_ context.Context, entries []p.VisibilityEntry) commonerrors.CategorizedError {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := make([]p.VisibilityEntry, len(entries))
	copy(clone, entries)
	s.batches = append(s.batches, clone)
	return nil
}
func (s *fakeVisibilityStore) ListRuns(context.Context, p.ListRunsQuery) (*p.ListRunsResult, commonerrors.CategorizedError) {
	return nil, nil
}
func (s *fakeVisibilityStore) DeleteAll(context.Context) error { return nil }
func (s *fakeVisibilityStore) Close() error                    { return nil }

func (s *fakeVisibilityStore) batchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.batches)
}

// fakeOpsShardManager is a minimal ShardManager that returns an unbounded
// context for GetCappedContext (no lease cap in tests).
type fakeOpsShardManager struct{}

func (m *fakeOpsShardManager) GetCappedContext(parent context.Context, _ int32) (context.Context, context.CancelFunc) {
	return context.WithCancel(parent)
}

// The remaining methods aren't called by OpsBatchReader — implement them as
// panics so test mistakes surface loudly.
func (m *fakeOpsShardManager) Start(context.Context) error { panic("not called") }
func (m *fakeOpsShardManager) Stop()                       { panic("not called") }
func (m *fakeOpsShardManager) GetOwnedShards() []int32     { panic("not called") }
func (m *fakeOpsShardManager) IsLocalShard(int32) bool     { panic("not called") }
func (m *fakeOpsShardManager) SignalShardLost(int32)       { panic("not called") }
func (m *fakeOpsShardManager) GetShardOwnerAddress(int32) string {
	panic("not called")
}
func (m *fakeOpsShardManager) AcquireImmediateTaskSeqLock(int32) (func(), commonerrors.CategorizedError) {
	panic("not called")
}
func (m *fakeOpsShardManager) GetNextImmediateTaskSeq(int32) (int64, error) {
	panic("not called")
}
func (m *fakeOpsShardManager) AcquireOpsFIFOTaskSeqLock(int32) (func(), commonerrors.CategorizedError) {
	panic("not called")
}
func (m *fakeOpsShardManager) GetNextOpsFIFOTaskSeq(int32) (int64, error) { panic("not called") }
func (m *fakeOpsShardManager) GetShardVersion(int32) int64                { panic("not called") }
func (m *fakeOpsShardManager) AwaitShardReady(context.Context, int32) commonerrors.CategorizedError {
	panic("not called")
}
func (m *fakeOpsShardManager) SetMetadataCallback(shardmanager.MetadataCallback) {
	panic("not called")
}

// ============================================================================
// Tests
// ============================================================================

func newOpsTestConfig() config.TaskProcessorConfig {
	cfg := config.DefaultTaskProcessorConfig()
	// Tighten timings for tests so we don't wait the production defaults.
	cfg.OpsBatchReadDelay = 10 * time.Millisecond
	cfg.OpsPollInterval = 50 * time.Millisecond
	cfg.OpsTaskRetryPolicy = backoff.RetryPolicy{
		InitialInterval:    5 * time.Millisecond,
		BackoffCoefficient: 2.0,
		MaximumInterval:    20 * time.Millisecond,
	}
	cfg.OpsBatchStuckWarnEvery = 1
	return cfg
}

// TestOpsBatchReader_HappyPath verifies that a full batch is split, merged,
// dispatched to both stores, and the committed offset advances to max(SortKey).
func TestOpsBatchReader_HappyPath(t *testing.T) {
	rs := &fakeOpsRunStore{}
	hs := &fakeHistoryStore{}
	vs := &fakeVisibilityStore{}
	sm := &fakeOpsShardManager{}
	logger := log.NewNoop()
	cfg := newOpsTestConfig()

	// Three rows: history, visibility (run "a"), visibility (run "a" again, status moves).
	rs.push(&p.OpsFIFOTaskRow{
		ShardID: 0, SortKey: 1, ID: ids.NewTaskID(), TaskType: p.OpsFIFOTaskHistoryWrite,
		HistoryPayload: &p.HistoryEvent{
			Namespace: "ns", RunID: "a", EventID: 1,
			Payload: p.HistoryEventPayload{RunStart: &pb.HistoryRunStartPayload{FlowType: "ft"}},
		},
		CreatedAt: time.Now(),
	})
	rs.push(&p.OpsFIFOTaskRow{
		ShardID: 0, SortKey: 2, ID: ids.NewTaskID(), TaskType: p.OpsFIFOTaskVisibilityWrite,
		VisibilityPayload: &p.VisibilityEntry{Namespace: "ns", RunID: "a", Status: p.RunStatusPending, StartTime: time.UnixMilli(100), UpdatedAt: time.UnixMilli(100)},
		CreatedAt:         time.Now(),
	})
	rs.push(&p.OpsFIFOTaskRow{
		ShardID: 0, SortKey: 3, ID: ids.NewTaskID(), TaskType: p.OpsFIFOTaskVisibilityWrite,
		VisibilityPayload: &p.VisibilityEntry{Namespace: "ns", RunID: "a", Status: p.RunStatusRunning, StartTime: time.UnixMilli(100), UpdatedAt: time.UnixMilli(200)},
		CreatedAt:         time.Now(),
	})

	shutdown := make(chan struct{})
	deleter := NewOpsBatchDeleter(0, cfg, rs, sm, logger, shutdown, 0)
	notif := NewShardTaskNotifier()
	reader := NewOpsBatchReader(0, cfg, rs, hs, vs, nil, deleter, sm, logger, shutdown, 0, notif.OpsFIFOCh())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go reader.Run(ctx)

	// Wait for both stores to receive a batch (success path).
	require.Eventually(t, func() bool {
		return hs.batchCount() >= 1 && vs.batchCount() >= 1
	}, 2*time.Second, 5*time.Millisecond, "history + visibility batches must arrive")

	// History batch contains the single history event (no merging there).
	hs.mu.Lock()
	assert.Len(t, hs.batches[0], 1, "history batch has 1 event")
	hs.mu.Unlock()

	// Visibility batch is MERGED to a single entry (both rows for run "a").
	vs.mu.Lock()
	if assert.Len(t, vs.batches[0], 1, "visibility entries for the same run merge to one upsert") {
		merged := vs.batches[0][0]
		assert.Equal(t, p.RunStatusRunning, merged.Status, "latest status wins")
		assert.True(t, merged.UpdatedAt.Equal(time.UnixMilli(200)))
	}
	vs.mu.Unlock()

	// Committed offset advanced to max(SortKey)=3.
	require.Eventually(t, func() bool {
		return deleter.GetWatermark() == 3
	}, 1*time.Second, 5*time.Millisecond, "committed offset advances to max SortKey of the batch")

	close(shutdown)
}

// TestOpsBatchReader_RetriesAndDoesNotAdvanceUntilSuccess validates the
// "no DLQ + skip" contract: a transient history-store failure causes the
// reader to retry the WHOLE batch indefinitely; the offset only moves
// after both downstream writes succeed, so on retry the visibility batch
// is sent again (relying on idempotency).
func TestOpsBatchReader_RetriesAndDoesNotAdvanceUntilSuccess(t *testing.T) {
	rs := &fakeOpsRunStore{}
	hs := &fakeHistoryStore{}
	hs.failNTimes.Store(2) // first two calls fail, third succeeds
	vs := &fakeVisibilityStore{}
	sm := &fakeOpsShardManager{}
	logger := log.NewNoop()
	cfg := newOpsTestConfig()

	// Push BOTH a history and a visibility row so the retry loop exercises
	// both downstream batch APIs (and we can assert visibility is replayed
	// alongside history while history is still failing).
	rs.push(&p.OpsFIFOTaskRow{
		ShardID: 0, SortKey: 1, ID: ids.NewTaskID(), TaskType: p.OpsFIFOTaskHistoryWrite,
		HistoryPayload: &p.HistoryEvent{
			Namespace: "ns", RunID: "a", EventID: 1,
			Payload: p.HistoryEventPayload{RunStart: &pb.HistoryRunStartPayload{}},
		},
		CreatedAt: time.Now(),
	})
	rs.push(&p.OpsFIFOTaskRow{
		ShardID: 0, SortKey: 2, ID: ids.NewTaskID(), TaskType: p.OpsFIFOTaskVisibilityWrite,
		VisibilityPayload: &p.VisibilityEntry{Namespace: "ns", RunID: "a", Status: p.RunStatusPending, StartTime: time.UnixMilli(100), UpdatedAt: time.UnixMilli(100)},
		CreatedAt:         time.Now(),
	})

	shutdown := make(chan struct{})
	deleter := NewOpsBatchDeleter(0, cfg, rs, sm, logger, shutdown, 0)
	notif := NewShardTaskNotifier()
	reader := NewOpsBatchReader(0, cfg, rs, hs, vs, nil, deleter, sm, logger, shutdown, 0, notif.OpsFIFOCh())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go reader.Run(ctx)

	// Eventually history sees a successful insert (after 2 failures).
	require.Eventually(t, func() bool {
		return hs.batchCount() >= 1
	}, 2*time.Second, 5*time.Millisecond, "history insert eventually succeeds")
	assert.GreaterOrEqual(t, int(hs.failedCount.Load()), 2,
		"reader retried at least the configured failure count")

	// Visibility was also called on every retry attempt because the writer
	// retries the WHOLE batch (idempotency contract). We expect at least
	// 3 calls: 2 retries during history failure + 1 successful round.
	assert.GreaterOrEqual(t, vs.batchCount(), 3,
		"visibility got called on each attempt (incl. failed history attempts) — got %d", vs.batchCount())

	// Committed offset only advances after both groups succeed (both rows
	// in the batch, so watermark = max SortKey = 2).
	require.Eventually(t, func() bool {
		return deleter.GetWatermark() == 2
	}, 1*time.Second, 5*time.Millisecond, "offset advances only after success")

	close(shutdown)
}

// TestOpsBatchReader_HistoryOnlyBatchSkipsVisibility confirms the writer
// doesn't issue an empty BatchUpsertVisibility call when no visibility
// rows are in the batch (and vice versa).
func TestOpsBatchReader_HistoryOnlyBatchSkipsVisibility(t *testing.T) {
	rs := &fakeOpsRunStore{}
	hs := &fakeHistoryStore{}
	vs := &fakeVisibilityStore{}
	sm := &fakeOpsShardManager{}
	logger := log.NewNoop()
	cfg := newOpsTestConfig()

	rs.push(&p.OpsFIFOTaskRow{
		ShardID: 0, SortKey: 1, ID: ids.NewTaskID(), TaskType: p.OpsFIFOTaskHistoryWrite,
		HistoryPayload: &p.HistoryEvent{
			Namespace: "ns", RunID: "a", EventID: 1,
			Payload: p.HistoryEventPayload{RunStart: &pb.HistoryRunStartPayload{}},
		},
		CreatedAt: time.Now(),
	})

	shutdown := make(chan struct{})
	deleter := NewOpsBatchDeleter(0, cfg, rs, sm, logger, shutdown, 0)
	notif := NewShardTaskNotifier()
	reader := NewOpsBatchReader(0, cfg, rs, hs, vs, nil, deleter, sm, logger, shutdown, 0, notif.OpsFIFOCh())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go reader.Run(ctx)

	require.Eventually(t, func() bool { return hs.batchCount() >= 1 },
		1*time.Second, 5*time.Millisecond)
	// Visibility never called.
	assert.Equal(t, 0, vs.batchCount(), "visibility batch must be skipped when no visibility tasks were in the read")
	assert.Equal(t, int64(1), deleter.GetWatermark())
	close(shutdown)
}
