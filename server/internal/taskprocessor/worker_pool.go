package taskprocessor

import (
	"context"
	"sync"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/common/utils/backoff"
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/internal/engine"
	"github.com/superdurable/dex/server/internal/metrics"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shardmanager"
)

// TaskCompletion carries the info needed by the batch deleter to remove a
// completed task from the pending set. Sent via channel from worker pool.
type TaskCompletion struct {
	SortKey int64
	ID      ids.TaskID
}

// TaskItem wraps a task row with its shard context for processing.
type TaskItem struct {
	ShardID int32
	Task    p.TaskRow
	// DoneCh receives a TaskCompletion after successful processing.
	// The per-shard batch deleter listens on this channel in its Run loop.
	// Nil if no tracking is needed (e.g., in tests).
	DoneCh chan<- TaskCompletion
	// TaskKey is sent to DoneCh on success.
	TaskKey TaskCompletion
}

// TaskHandler defines how each task type is processed.
type TaskHandler interface {
	HandleImmediateTask(ctx context.Context, shardID int32, task *p.ImmediateTaskRow) errors.CategorizedError
	HandleTimerTask(ctx context.Context, shardID int32, task *p.TimerTaskRow) errors.CategorizedError
}

// WorkerPool is an instance-level shared pool of goroutines that process tasks.
type WorkerPool struct {
	numWorkers               int
	taskChan                 chan *TaskItem
	handler                  TaskHandler
	logger                   log.Logger
	attemptTimeout           time.Duration
	immediateTaskRetryPolicy backoff.RetryPolicy
	timerTaskRetryPolicy     backoff.RetryPolicy
	wg                       sync.WaitGroup
	cancel                   context.CancelFunc

	dlqStore p.DLQStore
	memberID string // identifies which instance wrote the DLQ entry
}

func NewWorkerPool(numWorkers int, attemptTimeout time.Duration, immediateRetry, timerRetry backoff.RetryPolicy, handler TaskHandler, dlqStore p.DLQStore, memberID string, logger log.Logger) *WorkerPool {
	return &WorkerPool{
		numWorkers:               numWorkers,
		taskChan:                 make(chan *TaskItem, numWorkers*2),
		handler:                  handler,
		logger:                   logger,
		attemptTimeout:           attemptTimeout,
		immediateTaskRetryPolicy: immediateRetry,
		timerTaskRetryPolicy:     timerRetry,
		dlqStore:                 dlqStore,
		memberID:                 memberID,
	}
}

func (wp *WorkerPool) Start(ctx context.Context) {
	ctx, wp.cancel = context.WithCancel(ctx)
	for i := 0; i < wp.numWorkers; i++ {
		wp.wg.Add(1)
		go wp.worker(ctx)
	}
}

func (wp *WorkerPool) Stop() {
	if wp.cancel != nil {
		wp.cancel()
	}
	wp.wg.Wait()
}

func (wp *WorkerPool) Submit(item *TaskItem) {
	select {
	case wp.taskChan <- item:
		return
	default:
	}

	start := time.Now()
	wp.taskChan <- item
	metrics.LatencyWorkerPoolSubmitBlocked.Record(time.Since(start),
		taskQueueTypeTag(item))
}

func taskQueueTypeTag(item *TaskItem) metrics.Tag {
	if item.Task.Immediate != nil {
		return metrics.TagTaskQueueType(metrics.TaskQueueImmediate)
	}
	return metrics.TagTaskQueueType(metrics.TaskQueueTimer)
}

func (wp *WorkerPool) TaskChan() chan<- *TaskItem {
	return wp.taskChan
}

func (wp *WorkerPool) worker(ctx context.Context) {
	defer wp.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case item, ok := <-wp.taskChan:
			if !ok {
				return
			}
			wp.processItem(ctx, item)
		}
	}
}

func (wp *WorkerPool) processItem(ctx context.Context, item *TaskItem) {
	var runID, namespace string
	var taskType int
	switch {
	case item.Task.Immediate != nil:
		runID, namespace, taskType = item.Task.Immediate.TaskInfo.RunID, item.Task.Immediate.TaskInfo.Namespace, int(item.Task.Immediate.TaskType)
	case item.Task.Timer != nil:
		runID, namespace, taskType = item.Task.Timer.TaskInfo.RunID, item.Task.Timer.TaskInfo.Namespace, int(item.Task.Timer.TaskType)
	}
	wp.logger.Debug("WorkerPool.processItem start", tag.RunID(runID), tag.Namespace(namespace),
		tag.Shard(item.ShardID), tag.TaskID(item.TaskKey.ID.String()), tag.TaskType(taskType))

	err := wp.processWithRetry(ctx, item)
	if err != nil {
		wp.logger.Debug("WorkerPool.processItem failed", tag.RunID(runID), tag.Shard(item.ShardID),
			tag.TaskID(item.TaskKey.ID.String()), tag.TaskType(taskType), tag.Error(err))
	} else {
		wp.logger.Debug("WorkerPool.processItem done", tag.RunID(runID), tag.Shard(item.ShardID),
			tag.TaskID(item.TaskKey.ID.String()), tag.TaskType(taskType))
	}
	// Always notify completion (success or failure) so the pending set entry
	// is removed and the watermark can advance. Failed tasks remain recoverable
	// through higher-level mechanisms (heartbeat timeout re-creates dispatch tasks).
	//
	// For tasks that fail after retries, the DLQ captures the failure with full
	// diagnostic context so operators can inspect and replay them. Without the
	// DLQ, failed dispatch tasks for Pending runs would be silently lost.
	if err != nil {
		wp.writeToDLQ(ctx, item, err)
	}
	if item.DoneCh != nil {
		select {
		case item.DoneCh <- item.TaskKey:
		case <-ctx.Done():
			wp.logger.Warn("Shutdown before completion could be sent to DoneCh",
				tag.Shard(item.ShardID),
				tag.TaskID(item.TaskKey.ID.String()))
		}
	}
}

func (wp *WorkerPool) processWithRetry(ctx context.Context, item *TaskItem) errors.CategorizedError {
	typeTags := taskTypeTags(item)

	metrics.CounterTaskStarted.Inc(typeTags...)
	start := time.Now()

	// Record scheduling delay: time from when the task should have been processed
	// to when it was actually picked up.
	if item.Task.Immediate != nil && !item.Task.Immediate.CreatedAt.IsZero() {
		metrics.LatencyTaskScheduledToPickup.Record(time.Since(item.Task.Immediate.CreatedAt),
			metrics.TagTaskQueueType(metrics.TaskQueueImmediate))
	} else if item.Task.Timer != nil && item.Task.Timer.SortKey > 0 {
		fireAt := time.UnixMilli(item.Task.Timer.SortKey)
		metrics.LatencyTaskScheduledToPickup.Record(time.Since(fireAt),
			metrics.TagTaskQueueType(metrics.TaskQueueTimer))
	}

	policy := wp.retryPolicyForTask(item)
	retry := backoff.NewRetry(
		backoff.WithRetryPolicy(policy),
	)

	err := retry.DoCategorized(ctx, func(ctx context.Context) errors.CategorizedError {
		attemptCtx, attemptCancel := context.WithTimeout(ctx, wp.attemptTimeout)
		defer attemptCancel()

		if item.Task.Immediate != nil {
			return wp.handler.HandleImmediateTask(attemptCtx, item.ShardID, item.Task.Immediate)
		}
		if item.Task.Timer != nil {
			return wp.handler.HandleTimerTask(attemptCtx, item.ShardID, item.Task.Timer)
		}
		return nil
	})

	metrics.LatencyTaskExecution.Record(time.Since(start), typeTags...)

	if err != nil {
		metrics.CounterTaskFailed.Inc(typeTags...)
		wp.logTaskFailure(item, err)
	} else {
		metrics.CounterTaskSucceeded.Inc(typeTags...)
	}
	return err
}

// retryPolicyForTask returns the configured retry policy for the task type.
// Immediate tasks use ImmediateTaskRetryPolicy (shorter, re-discoverable from DB).
// Timer tasks use TimerTaskRetryPolicy (longer, unique fire times).
func (wp *WorkerPool) retryPolicyForTask(item *TaskItem) *backoff.RetryPolicy {
	if item.Task.Immediate != nil {
		return &wp.immediateTaskRetryPolicy
	}
	return &wp.timerTaskRetryPolicy
}

// taskTypeTags returns the metric tags for a task item, using enum-based tags
// for both the queue type and the specific task type.
func taskTypeTags(item *TaskItem) []metrics.Tag {
	if item.Task.Immediate != nil {
		return []metrics.Tag{
			metrics.TagTaskQueueType(metrics.TaskQueueImmediate),
			metrics.TagImmediateTaskType(item.Task.Immediate.TaskType),
		}
	}
	if item.Task.Timer != nil {
		return []metrics.Tag{
			metrics.TagTaskQueueType(metrics.TaskQueueTimer),
			metrics.TagTimerTaskType(item.Task.Timer.TaskType),
		}
	}
	return nil
}

// logTaskFailure logs full task details for manual replay after bug fix.
// Retriable errors (exhausted 1h retries) = Error level (infrastructure issue, needs intervention).
// Non-retriable errors = Warn level (business logic, may self-resolve or be expected).
func (wp *WorkerPool) logTaskFailure(item *TaskItem, err errors.CategorizedError) {
	tags := []tag.Tag{tag.Shard(item.ShardID), tag.Error(err)}

	if item.Task.Immediate != nil {
		t := item.Task.Immediate
		tags = append(tags, tag.TaskID(t.ID.String()), tag.TaskType(int32(t.TaskType)),
			tag.RunID(t.TaskInfo.RunID), tag.Namespace(t.TaskInfo.Namespace),
			tag.JsonValue(t.TaskInfo))
	} else if item.Task.Timer != nil {
		t := item.Task.Timer
		tags = append(tags, tag.TaskID(t.ID.String()), tag.TaskType(int32(t.TaskType)),
			tag.RunID(t.TaskInfo.RunID), tag.Namespace(t.TaskInfo.Namespace),
			tag.JsonValue(t.TaskInfo))
	}

	if err.IsRetriable() {
		wp.logger.Error("Task permanently failed after exhausting retries, details logged for manual replay", tags...)
	} else {
		wp.logger.Warn("Task failed with non-retriable error, details logged for manual replay", tags...)
	}
}

// writeToDLQ writes a failed task to the dead letter queue.
// Handles both immediate and timer tasks. Retries indefinitely with
// exponential backoff — the DLQ is the last safety net and must not lose tasks.
// Each failed attempt increments CounterTaskDLQWriteFailed for alerting.
func (wp *WorkerPool) writeToDLQ(_ context.Context, item *TaskItem, taskErr errors.CategorizedError) {
	entry := wp.buildDLQEntry(item, taskErr)
	if entry == nil {
		return
	}

	retryInterval := 500 * time.Millisecond
	const maxRetryInterval = 30 * time.Second
	for {
		dlqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		dlqErr := wp.dlqStore.WriteDLQ(dlqCtx, entry)
		cancel()

		if dlqErr == nil {
			break
		}
		metrics.CounterTaskDLQWriteFailed.Inc()
		wp.logger.Error("Failed to write task to DLQ, retrying",
			tag.Shard(item.ShardID),
			tag.TaskID(entry.TaskID.String()),
			tag.RunID(entry.RunID),
			tag.Error(dlqErr))
		time.Sleep(retryInterval)
		if retryInterval < maxRetryInterval {
			retryInterval *= 2
		}
	}

	metrics.CounterTaskDLQWritten.Inc()
	wp.logger.Warn("Task written to DLQ after exhausting retries",
		tag.Shard(item.ShardID),
		tag.TaskID(entry.TaskID.String()),
		tag.RunID(entry.RunID),
		tag.Namespace(entry.Namespace),
		tag.TaskListName(entry.TaskListName),
		tag.Error(taskErr))
}

func (wp *WorkerPool) buildDLQEntry(item *TaskItem, taskErr errors.CategorizedError) *p.DLQEntry {
	base := &p.DLQEntry{
		ShardID:       item.ShardID,
		Error:         taskErr.Error(),
		ErrorCategory: string(taskErr.GetCategory()),
		MemberID:      wp.memberID,
	}
	if t := item.Task.Immediate; t != nil {
		base.TaskID = t.ID
		base.QueueType = p.RowTypeImmediateTask
		base.TaskType = int32(t.TaskType)
		base.RunID = t.TaskInfo.RunID
		base.Namespace = t.TaskInfo.Namespace
		base.TaskListName = t.TaskInfo.TaskListName
		base.SortKey = t.SortKey
		base.CreatedAt = t.CreatedAt
		return base
	}
	if t := item.Task.Timer; t != nil {
		base.TaskID = t.ID
		base.QueueType = p.RowTypeTimerTask
		base.TaskType = int32(t.TaskType)
		base.RunID = t.TaskInfo.RunID
		base.Namespace = t.TaskInfo.Namespace
		base.TaskListName = ""
		base.SortKey = t.SortKey
		base.CreatedAt = t.CreatedAt
		return base
	}
	return nil
}

// DefaultTaskHandler routes tasks to the RunEngine and MatchingService.
// It caps the context per-shard before calling RunEngine to ensure no store
// operation can outlive the shard lease (fail-fast on lease expiry).
type DefaultTaskHandler struct {
	RunEngine    engine.RunEngine
	ShardManager shardmanager.ShardManager
	// LocalMatchingClient is the loopback client to this node's own matching
	// service, used to dispatch runs (run -> matching).
	LocalMatchingClient pb.MatchingServiceClient
	Logger              log.Logger
}

func (h *DefaultTaskHandler) HandleImmediateTask(ctx context.Context, shardID int32, task *p.ImmediateTaskRow) errors.CategorizedError {
	ctx, cancel := h.ShardManager.GetCappedContext(ctx, shardID)
	defer cancel()

	switch task.TaskType {
	case p.ImmediateTaskRunInitialDispatch, p.ImmediateTaskRunResumeDispatch:
		return h.handleDispatchTask(ctx, shardID, task)
	default:
		h.Logger.Warn("Unknown immediate task type")
		return nil
	}
}

func (h *DefaultTaskHandler) handleDispatchTask(ctx context.Context, shardID int32, task *p.ImmediateTaskRow) errors.CategorizedError {
	if h.LocalMatchingClient == nil {
		h.Logger.Warn("LocalMatchingClient not configured, skipping dispatch task", tag.Shard(shardID))
		return nil
	}

	h.Logger.Info("Dispatch task started",
		tag.Shard(shardID),
		tag.RunID(task.TaskInfo.RunID),
		tag.Namespace(task.TaskInfo.Namespace),
		tag.TaskListName(task.TaskInfo.TaskListName))

	// Open bidi stream to matching service.
	stream, streamErr := h.LocalMatchingClient.DispatchRun(ctx)
	if streamErr != nil {
		h.Logger.Warn("DispatchRun stream open failed",
			tag.Shard(shardID),
			tag.RunID(task.TaskInfo.RunID),
			tag.TaskListName(task.TaskInfo.TaskListName),
			tag.Error(streamErr))
		return errors.NewUnavailableError("DispatchRun stream open failed", streamErr)
	}

	// Step 1: Send DispatchRunRequest.
	if err := stream.Send(&pb.EngineToMatchingDispatchMessage{
		Message: &pb.EngineToMatchingDispatchMessage_Request{
			Request: &pb.DispatchRunRequest{
				Namespace:          task.TaskInfo.Namespace,
				TaskListName:       task.TaskInfo.TaskListName,
				RunId:              task.TaskInfo.RunID,
				ShardId:            shardID,
				TaskId:             task.ID.String(),
				DurableTimerFireAt: task.TaskInfo.DurableTimerFireAt,
			},
		},
	}); err != nil {
		return errors.NewUnavailableError("DispatchRun send request failed", err)
	}

	// Step 2: Recv DispatchRunResponse.
	respMsg, recvErr := stream.Recv()
	if recvErr != nil {
		return errors.NewUnavailableError("DispatchRun recv response failed", recvErr)
	}
	resp := respMsg.GetResponse()
	if resp == nil {
		return errors.NewInvalidInputError("DispatchRun: expected DispatchRunResponse, got different message type", nil)
	}

	h.Logger.Debugf("handleDispatchTask: DispatchRun OK shard=%d run=%s syncMatched=%v workerID=%s",
		shardID, task.TaskInfo.RunID, resp.SyncMatched, resp.WorkerId)

	// Step 3: Transition run status based on dispatch result.
	// HandleRunDispatchResult CAS-transitions the run to Running (if
	// syncMatched) or WaitingForWorker (if async miss), writes the
	// workerID, sets the heartbeat timer, and returns the PollForRunResponse
	// data for the worker.
	pollResp, casErr := h.RunEngine.HandleRunDispatchResult(ctx, shardID, task.TaskInfo.Namespace, task.TaskInfo.RunID, resp.SyncMatched, resp.WorkerId)
	if casErr != nil {
		h.Logger.Debugf("handleDispatchTask: HandleRunDispatchResult FAILED shard=%d run=%s err=%v",
			shardID, task.TaskInfo.RunID, casErr)
		return casErr
	}

	// Step 4: If sync matched, send PollForRunResponse (msg3) so matching
	// delivers it to the waiting worker. If the send fails, the run is
	// already Running with a heartbeat timer — the timer will eventually
	// detect the stuck run and re-dispatch. No retry needed.
	if resp.SyncMatched && pollResp != nil {
		if err := stream.Send(&pb.EngineToMatchingDispatchMessage{
			Message: &pb.EngineToMatchingDispatchMessage_PollForRunResponse{
				PollForRunResponse: pollResp,
			},
		}); err != nil {
			h.Logger.Warn("PollForRunResponse send failed after run transitioned to Running; heartbeat timer will recover",
				tag.Shard(shardID),
				tag.RunID(task.TaskInfo.RunID),
				tag.Error(err))
			return nil
		}
	}

	stream.CloseSend()
	return nil
}

func (h *DefaultTaskHandler) HandleTimerTask(ctx context.Context, shardID int32, task *p.TimerTaskRow) errors.CategorizedError {
	ctx, cancel := h.ShardManager.GetCappedContext(ctx, shardID)
	defer cancel()

	switch task.TaskType {
	case p.TimerTaskRunHeartbeat:
		return h.RunEngine.HandleHeartbeatTimeout(ctx, shardID, &engine.HeartbeatTimerFiredRequest{
			RunID:     task.TaskInfo.RunID,
			Namespace: task.TaskInfo.Namespace,
			TimerID:   task.ID,
		})
	case p.TimerTaskStepWaitForTimer:
		return h.RunEngine.HandleStepWaitForTimerFired(ctx, shardID, &engine.StepWaitForTimerFiredRequest{
			RunID:        task.TaskInfo.RunID,
			Namespace:    task.TaskInfo.Namespace,
			TimerID:      task.ID,
			FireAtUnixMs: task.SortKey,
		})
	default:
		h.Logger.Warn("Unknown timer task type")
		return nil
	}
}
