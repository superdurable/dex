package shardmanager

import (
	"context"

	"github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// TaskNotifier is the wake-up hint surface used by ShardedRunStore after a
// successful task write. Implementations (taskprocessor.LocalTaskNotifier)
// route the hint to the per-shard batch reader's wait loop. The interface
// lives in shardmanager so engine code does not need to import any
// notification machinery — it gets notifications "for free" from the wrapper.
type TaskNotifier interface {
	// NotifyNewImmediateTask wakes the shard's immediate batch reader so it
	// skips its poll interval. Best-effort.
	NotifyNewImmediateTask(shardID int32)
	// NotifyNewTimerTask wakes the shard's timer batch reader and asks it to
	// advance its next-wakeup time to fireAtUnixMs if that would be sooner
	// than what it had planned. Best-effort.
	NotifyNewTimerTask(shardID int32, fireAtUnixMs int64)
	// NotifyNewOpsFIFOTask wakes the shard's OpsFIFO batch reader.
	// Best-effort (single-buffer channel, drops duplicates).
	NotifyNewOpsFIFOTask(shardID int32)
}

// ShardedRunStore decorates persistence.RunStore with shard-aware concerns
// the engine should not have to know about:
//
//  1. AwaitShardReady before every write (clock-skew gate from claim time).
//  2. AcquireImmediateTaskSeqLock + GetNextImmediateTaskSeq for any
//     ImmediateTaskRow whose SortKey is the sentinel zero, AND
//     AcquireOpsFIFOTaskSeqLock + GetNextOpsFIFOTaskSeq for any
//     OpsFIFOTaskRow whose SortKey is the sentinel zero. Multiple tasks of
//     the same kind in one batch are allocated in order under the same
//     lock. Each lock is held across the underlying RunStore write and only
//     released after it returns: the per-queue batch readers scan by
//     afterSeq and assume any visible seq=k implies all seq<=k are
//     committed, so commits MUST land in the same order as seq allocation.
//     The two locks are independent so immediate-task and OpsFIFO writers
//     do not contend.
//  3. After a successful write, signal LocalTaskNotifier per task kind:
//     NotifyNewImmediateTask once if any immediate row was written;
//     NotifyNewTimerTask(fireAt) for each timer row;
//     NotifyNewOpsFIFOTask once if any OpsFIFO row was written.
//
// Engine code calls only these three methods, building rows with SortKey == 0
// for immediate / OpsFIFO tasks (sentinel meaning "needs allocation") and
// the actual fire_at for timer tasks.
type ShardedRunStore interface {
	// CreateRunWithTasks atomically inserts a run + tasks. Reads shardID from
	// run.ShardID. Allocates SortKey for any immediate tasks with the zero
	// sentinel.
	CreateRunWithTasks(ctx context.Context, run *p.RunRow, tasks []p.TaskRow) errors.CategorizedError

	// UpdateRunWithNewTasks does a CAS update on the run + atomically inserts
	// new tasks. Same SortKey allocation behavior as CreateRunWithTasks.
	UpdateRunWithNewTasks(ctx context.Context, shardID int32, namespace, runID string,
		expectedVersion int64, update *p.RunRowUpdate, tasks []p.TaskRow) errors.CategorizedError

	// GetRun is a pass-through to the underlying RunStore for read-only use.
	GetRun(ctx context.Context, shardID int32, namespace, runID string, opts p.GetRunOptions) (*p.RunRow, errors.CategorizedError)
}

// NewShardedRunStore wires a RunStore with the shard manager (for await-ready
// + seq lock + seq allocation) and a notifier (for post-write wake-ups).
// Pass notifier=nil in tests that don't exercise the wake-up path.
func NewShardedRunStore(runStore p.RunStore, sm ShardManager, notifier TaskNotifier) ShardedRunStore {
	return &shardedRunStoreImpl{
		runStore: runStore,
		sm:       sm,
		notifier: notifier,
	}
}

type shardedRunStoreImpl struct {
	runStore p.RunStore
	sm       ShardManager
	notifier TaskNotifier
}

func (s *shardedRunStoreImpl) CreateRunWithTasks(ctx context.Context, run *p.RunRow, tasks []p.TaskRow) errors.CategorizedError {
	return s.write(ctx, run.ShardID, tasks, func() errors.CategorizedError {
		return s.runStore.CreateRunWithTasks(ctx, run, tasks)
	})
}

func (s *shardedRunStoreImpl) UpdateRunWithNewTasks(ctx context.Context, shardID int32, namespace, runID string,
	expectedVersion int64, update *p.RunRowUpdate, tasks []p.TaskRow) errors.CategorizedError {
	return s.write(ctx, shardID, tasks, func() errors.CategorizedError {
		return s.runStore.UpdateRunWithNewTasks(ctx, shardID, namespace, runID, expectedVersion, update, tasks)
	})
}

func (s *shardedRunStoreImpl) GetRun(ctx context.Context, shardID int32, namespace, runID string, opts p.GetRunOptions) (*p.RunRow, errors.CategorizedError) {
	return s.runStore.GetRun(ctx, shardID, namespace, runID, opts)
}

// write wraps a RunStore write with the three shard-aware concerns:
// (1) await shard ready, (2) optionally acquire seq locks + assign SortKeys
// for immediate AND OpsFIFO tasks, (3) call do (which is bound to the
// underlying RunStore call), and on success (4) fan out wake-up hints. doFn
// closes over the tasks slice so SortKey backfilling done here is visible to
// the underlying write.
//
// Lock ordering: when both immediate AND OpsFIFO tasks need seq allocation
// in the same write, we acquire the immediate lock first then the OpsFIFO
// lock. This is a fixed global order so two writers that always follow it
// cannot deadlock even if they hold one lock and want the other. Both locks
// are released (via defer) AFTER the underlying RunStore write returns — see
// the long comment below for the monotonic-visibility correctness argument.
func (s *shardedRunStoreImpl) write(ctx context.Context, shardID int32, tasks []p.TaskRow, do func() errors.CategorizedError) errors.CategorizedError {
	if err := s.sm.AwaitShardReady(ctx, shardID); err != nil {
		return err
	}

	needsImmediateSeq := false
	needsOpsFIFOSeq := false
	for i := range tasks {
		if tasks[i].Immediate != nil && tasks[i].Immediate.SortKey == 0 {
			needsImmediateSeq = true
		}
		if tasks[i].OpsFIFO != nil && tasks[i].OpsFIFO.SortKey == 0 {
			needsOpsFIFOSeq = true
		}
	}

	if needsImmediateSeq {
		// The seq lock MUST span allocation AND the underlying DB write.
		// The immediate batch reader scans by `afterSeq` and assumes any
		// visible seq=k implies all seq <= k are committed. If we unlocked
		// after allocation but before the write, two writers could interleave
		// their commits in the opposite order from their seq allocation:
		//
		//   T1: allocate seq=5
		//   T1: unlock                          (premature)
		//   T2: allocate seq=6
		//   T2: runStore write commits seq=6 first
		//   T1: runStore write commits seq=5 later
		//
		// The reader observes seq=6, advances lastSortKey to 6, and the
		// late-committing seq=5 is permanently skipped → the run never gets
		// dispatched. So `defer unlock()` MUST wrap the runStore call.
		unlock, lockErr := s.sm.AcquireImmediateTaskSeqLock(shardID)
		if lockErr != nil {
			return lockErr
		}
		defer unlock()

		for i := range tasks {
			if tasks[i].Immediate == nil || tasks[i].Immediate.SortKey != 0 {
				continue
			}
			seq, err := s.sm.GetNextImmediateTaskSeq(shardID)
			if err != nil {
				return errors.NewInternalError("failed to allocate TaskSeq", err)
			}
			tasks[i].Immediate.SortKey = seq
		}
	}

	if needsOpsFIFOSeq {
		// Same monotonic-visibility argument as the immediate lock above.
		// The lock is independent so the OpsFIFO outbox writer never blocks
		// on the immediate-task seq path (which is on the hot dispatch
		// critical section), and vice-versa. Acquired AFTER the immediate
		// lock so the global lock order is fixed and deadlock-free.
		unlockOpsFIFO, lockErr := s.sm.AcquireOpsFIFOTaskSeqLock(shardID)
		if lockErr != nil {
			return lockErr
		}
		defer unlockOpsFIFO()

		for i := range tasks {
			if tasks[i].OpsFIFO == nil || tasks[i].OpsFIFO.SortKey != 0 {
				continue
			}
			seq, err := s.sm.GetNextOpsFIFOTaskSeq(shardID)
			if err != nil {
				return errors.NewInternalError("failed to allocate OpsFIFO TaskSeq", err)
			}
			tasks[i].OpsFIFO.SortKey = seq
		}
	}

	if err := do(); err != nil {
		return err
	}
	s.signalTasks(shardID, tasks)
	return nil
}

// signalTasks fans out wake-up hints based on the kinds of tasks just
// written. For immediate / OpsFIFO tasks we send a single doorbell each;
// for timer tasks we send one fire-time-aware hint per row so the reader
// can advance its next-wakeup time to the soonest pending timer.
// Best-effort: notifier is allowed to be nil (tests) and individual sends
// are non-blocking inside the notifier.
func (s *shardedRunStoreImpl) signalTasks(shardID int32, tasks []p.TaskRow) {
	if s.notifier == nil || len(tasks) == 0 {
		return
	}
	var anyImmediate, anyOpsFIFO bool
	for i := range tasks {
		if tasks[i].Immediate != nil {
			anyImmediate = true
		}
		if tasks[i].OpsFIFO != nil {
			anyOpsFIFO = true
		}
		if tasks[i].Timer != nil {
			s.notifier.NotifyNewTimerTask(shardID, tasks[i].Timer.SortKey)
		}
	}
	if anyImmediate {
		s.notifier.NotifyNewImmediateTask(shardID)
	}
	if anyOpsFIFO {
		s.notifier.NotifyNewOpsFIFOTask(shardID)
	}
}
