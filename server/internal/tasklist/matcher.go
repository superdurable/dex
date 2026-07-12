package tasklist

import (
	"context"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
)

// Task represents a single dispatch task in the matcher's pipeline.
//
// Two flavors:
//   - Async-pickup task: has a non-zero taskID (from DB). Comes through
//     the matcher's bufferedCh after the reader fetched it.
//   - Sync-match task: taskID == 0 (in-memory only, not persisted). Comes
//     through the matcher's syncOfferCh as a rendezvous handoff from
//     AddTask.
type Task struct {
	runID     string
	namespace string
	shardID   int32
	taskID    int64 // > 0 for async pickup; 0 for sync match

	// pollRespDeliveryCh is used in the sync-match path: the DispatchRun caller
	// (runsService) builds the PollForRunResponse after marking the run as
	// Running, then pushes it here.
	// Nil for async-pickup tasks.
	// Async-pickup call ProcessAsyncMatch to get the PollResponse.
	pollRespDeliveryCh chan *pb.PollForRunResponse

	// workerIDCh is is used in the sync-match path: the poller's PollForRun handler
	// publishes its workerID after picking up a sync-match task. The
	// DispatchRun handler reads and populate DispatchRunResponse.worker_id.
	// Nil for async-pickup tasks
	// async-pickup is handled in PollForRun, which already knows its own workerID
	// directly.
	workerIDCh chan string
}

// RunID returns the run ID this task targets.
func (t *Task) RunID() string { return t.runID }

// TaskID returns the underlying DB taskID (0 for sync-match tasks).
func (t *Task) TaskID() int64 { return t.taskID }

// Namespace returns the namespace of the run this task targets.
func (t *Task) Namespace() string { return t.namespace }

// ShardID returns the shard owning the run.
func (t *Task) ShardID() int32 { return t.shardID }

// IsSyncMatch reports whether this task originated from a DispatchRun
// sync-match path (caller will push PollForRunResponse via pollRespDeliveryCh).
func (t *Task) IsSyncMatch() bool { return t.taskID == 0 }

// PollDeliveryCh returns the channel the sync-match caller uses to push
// the PollForRunResponse. Always non-nil for sync-match tasks; nil otherwise.
func (t *Task) PollDeliveryCh() chan *pb.PollForRunResponse { return t.pollRespDeliveryCh }

// WorkerIDCh returns the back-channel for the poller to publish its
// workerID. Always non-nil for sync-match tasks; nil otherwise.
func (t *Task) WorkerIDCh() chan string { return t.workerIDCh }

// matcher is the rendezvous point between AddTask producers and
// PollForTask consumers.
//
// Two task sources:
//   - syncOfferCh: unbuffered rendezvous for sync-match handoffs. The
//     producer is matcher.TryLocalOffer, called from manager.AddTask. A
//     non-blocking send succeeds only when a receiver (matcher.Poll's
//     select) is currently waiting.
//   - bufferedCh: buffered FIFO for async-pickup tasks. The producer is
//     the taskReader (which fetches from DB and pushes here). The
//     buffer's capacity is TaskBufferSize.
type matcher struct {
	id        *Identifier
	logger    log.Logger
	forwarder pollForwarder

	syncOfferCh chan *Task // unbuffered: rendezvous handoff
	bufferedCh  chan *Task // buffered
}

func newMatcher(id *Identifier, fwd pollForwarder, logger log.Logger, bufferSize int) *matcher {
	if bufferSize < 1 {
		bufferSize = 100
	}
	return &matcher{
		id:          id,
		logger:      logger.WithTags(tag.Namespace(id.Namespace()), tag.TaskListName(id.FullName())),
		forwarder:   fwd,
		syncOfferCh: make(chan *Task),
		bufferedCh:  make(chan *Task, bufferSize),
	}
}

func (m *matcher) BufferCh() chan *Task { return m.bufferedCh }

// TryLocalOffer attempts a non-blocking LOCAL sync-match handoff. Returns true
// if a poller's matcher.Poll select committed to the syncOfferCh case;
// false if no poller is waiting.
func (m *matcher) TryLocalOffer(task *Task) bool {
	select {
	case m.syncOfferCh <- task:
		m.logger.Debug("matcher.TryLocalOffer local sync-match", tag.RunID(task.runID), tag.Shard(task.shardID))
		return true
	default:
		m.logger.Debug("matcher.TryLocalOffer miss", tag.RunID(task.runID), tag.Shard(task.shardID))
		return false
	}
}

// TryLocalPoll attempts a non-blocking task pickup from local sources only
func (m *matcher) TryLocalPoll() (*Task, bool) {
	select {
	case task := <-m.syncOfferCh:
		m.logger.Debug("matcher.TryLocalPoll picked sync-match", tag.RunID(task.runID), tag.Shard(task.shardID))
		return task, true
	case task := <-m.bufferedCh:
		m.logger.Debug("matcher.TryLocalPoll picked async", tag.RunID(task.runID), tag.Shard(task.shardID))
		return task, true
	default:
		return nil, false
	}
}

// FullPoll waits for a task from any source for this worker's long-poll.
//
// Sources:
//   - syncOfferCh (sync match handoff from TryLocalOffer)
//   - bufferedCh (async pickup pushed by reader)
//   - forwarded poll to root (non-root only)
//   - ctx.Done (long-poll timeout)
//
// on long-poll timeout, return nil, nil
func (m *matcher) FullPoll(ctx context.Context, workerID string) (*Task, errors.CategorizedError) {
	if task, ok := m.TryLocalPoll(); ok {
		return task, nil
	}

	if m.id.IsRoot() {
		// Root: no fan-in. Block on local sources only.
		return m.blockLocalPoll(ctx)
	}

	// Non-root: try forward to root once (root replies non-blockingly). Consume the
	// result inline — never drop a task root already committed.
	if task, err := m.forwarder.ForwardPoll(ctx, workerID); err == nil && task != nil {
		m.logger.Debugf("matcher.Poll: forwarded hit run_id=%s", task.runID)
		return task, nil
	}
	// here ignores the error from forwarding because we fallback to blockingLocalPoll

	// Root had nothing (or forward failed) — wait out the rest locally.
	return m.blockLocalPoll(ctx)
}

// blockLocalPoll blocks on the two local task sources until ctx expires.
func (m *matcher) blockLocalPoll(ctx context.Context) (*Task, errors.CategorizedError) {
	select {
	case task := <-m.syncOfferCh:
		m.logger.Debugf("matcher.Poll: syncOffer hit run_id=%s task_id=%d", task.runID, task.taskID)
		return task, nil
	case task := <-m.bufferedCh:
		m.logger.Debugf("matcher.Poll: buffered hit run_id=%s task_id=%d", task.runID, task.taskID)
		return task, nil
	case <-ctx.Done():
		return nil, nil
	}
}

// newAsyncPickupTask constructs a Task for the async-pickup path.
// The reader uses this when fetching rows from DB.
func newAsyncPickupTask(runID, namespace string, shardID int32, taskID int64) *Task {
	return &Task{
		runID:     runID,
		namespace: namespace,
		shardID:   shardID,
		taskID:    taskID,
	}
}

// newSyncMatchTask constructs a Task for the sync-match path. Both
// rendezvous channels are buffered(1) so neither side blocks once the
// rendezvous itself has succeeded.
func newSyncMatchTask(runID, namespace string, shardID int32) *Task {
	return &Task{
		runID:              runID,
		namespace:          namespace,
		shardID:            shardID,
		taskID:             0,
		pollRespDeliveryCh: make(chan *pb.PollForRunResponse, 1),
		workerIDCh:         make(chan string, 1),
	}
}
