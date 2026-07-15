package engine

import (
	"context"
	"fmt"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/engine/blobs"
	"github.com/superdurable/dex/server/internal/engine/mutation"
	"github.com/superdurable/dex/server/internal/engine/pbconv"
	"github.com/superdurable/dex/server/internal/metrics"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shardmanager"
)

// RunEngine is the core state machine for run execution.
type RunEngine interface {
	// GetRun reads the full run state with blob refs resolved.
	// Returns (nil, nil) if the run's status doesn't match any of the given filter values.
	GetRun(ctx context.Context, namespace, runID string, statusFilter []p.RunStatus) (*pb.GetRunResponse, errors.CategorizedError)

	// client APIs

	StartRun(ctx context.Context, req *pb.StartRunRequest) errors.CategorizedError
	// StopRun CAS-transitions the run to Completed or Failed per stop_decision
	// when it is in any non-terminal state. reason is optional user text for history.
	StopRun(ctx context.Context, namespace, runID string, stopDecision pb.StopDecision, reason string) (wasActive bool, taskListName string, workerID string, err errors.CategorizedError)
	PublishExternalChannelMessages(ctx context.Context, shardID int32, req *pb.PublishToChannelRequest) errors.CategorizedError
	ForkRun(ctx context.Context, req *pb.ForkRunRequest) (previousWorkerID string, err errors.CategorizedError)

	// worker APIs (ProcessXyz)

	ProcessStepExecuteCompleted(ctx context.Context, shardID int32, namespace string, req *pb.StepExecuteCompletedRequest) (*pb.WorkerCallResponse, errors.CategorizedError)
	ProcessStepWaitForCompleted(ctx context.Context, shardID int32, namespace string, req *pb.StepWaitForCompletedRequest) (*pb.WorkerCallResponse, errors.CategorizedError)
	// ProcessStepsUnblocked checkpoints worker-driven sibling unblocks that
	// happened OUT of any step completion
	ProcessStepsUnblocked(ctx context.Context, shardID int32, req *pb.StepsUnblockedRequest) (*pb.WorkerCallResponse, errors.CategorizedError)
	// ProcessRecordHeartbeat is the worker's heartbeat call.
	ProcessRecordHeartbeat(ctx context.Context, shardID int32, req *pb.ProcessRecordHeartbeatRequest) (*pb.WorkerCallResponse, errors.CategorizedError)
	// ProcessReleaseRun releases a run from the active worker.
	ProcessReleaseRun(ctx context.Context, shardID int32, req *pb.ProcessReleaseRunRequest) (*pb.ProcessReleaseRunResponse, errors.CategorizedError)

	// internal APIs (HandleXyz)

	// HandleRunDispatchResult transitions a run after the DispatchRun RPC
	// completes. transitionToRunning=true (sync match): transitions to Running,
	// sets WorkerID, and creates a heartbeat timer;
	HandleRunDispatchResult(ctx context.Context, shardID int32, namespace, runID string, transitionToRunning bool, workerID string) (*pb.PollForRunResponse, errors.CategorizedError)
	// BatchProcessAsyncMatch transitions multiple runs to Running for async
	// match pickup.
	HandleBatchAsyncMatch(ctx context.Context, requests []AsyncMatchRequest) map[string]bool
	HandleHeartbeatTimeout(ctx context.Context, shardID int32, req *HeartbeatTimerFiredRequest) errors.CategorizedError
	HandleStepWaitForTimerFired(ctx context.Context, shardID int32, req *StepWaitForTimerFiredRequest) errors.CategorizedError
}

// some internal types
type (
	// HeartbeatTimerFiredRequest is the input to RunEngine.ProcessHeartbeatTimerFired.
	HeartbeatTimerFiredRequest struct {
		RunID     string
		Namespace string
		TimerID   ids.TaskID // the task ID that fired
	}

	// StepWaitForTimerFiredRequest is the input to RunEngine.ProcessStepWaitForTimerFired.
	StepWaitForTimerFiredRequest struct {
		RunID        string
		Namespace    string
		TimerID      ids.TaskID // the task ID that fired
		FireAtUnixMs int64      // the timer task's SortKey (= DurableTimerFireAt when the timer was created)
	}
)

type runEngineImpl struct {
	cfg          *config.RunServiceConfig
	runStore     shardmanager.ShardedRunStore
	historyStore p.HistoryStore
	blobs        *blobs.BlobsFactory
	shardMapper  shardmanager.ShardMapper
	shardManager shardmanager.ShardManager
	logger       log.Logger
	mutations    *mutation.Factory
}

// NewRunEngine constructs the run engine.
func NewRunEngine(
	cfg *config.RunServiceConfig,
	runStore shardmanager.ShardedRunStore,
	historyStore p.HistoryStore,
	blobStore p.BlobStore,
	shardMapper shardmanager.ShardMapper,
	shardManager shardmanager.ShardManager,
	logger log.Logger,
) RunEngine {
	if historyStore == nil {
		panic("NewRunEngine: historyStore must not be nil")
	}
	return &runEngineImpl{
		cfg:          cfg,
		runStore:     runStore,
		historyStore: historyStore,
		blobs:        blobs.New(blobStore),
		shardMapper:  shardMapper,
		shardManager: shardManager,
		logger:       logger,
		mutations: mutation.NewFactory(mutation.Deps{
			RunStore:                   runStore,
			Logger:                     logger,
			HeartbeatTimerDuration:     cfg.HeartbeatTimerDuration,
			StepRetryLastErrorMaxBytes: cfg.StepRetryLastErrorMaxBytes,
		}),
	}
}

// ============================================================================
// StartRun
// ============================================================================

func (e *runEngineImpl) StartRun(ctx context.Context, req *pb.StartRunRequest) errors.CategorizedError {
	e.logger.Debug("RunEngine.StartRun", tag.RunID(req.RunId), tag.Namespace(req.Namespace), tag.TaskListName(req.TaskListName))
	shardID := e.shardMapper.GetShardID(req.Namespace, req.RunId)

	uploader := e.blobs.NewUploader(shardID, req.Namespace, req.RunId)
	stepInputs := make([]*pb.Value, len(req.StartingSteps))
	for index, startingStep := range req.StartingSteps {
		stepInputs[index] = startingStep.Input
	}
	convertedInputs := uploader.Slice(stepInputs)

	var startingSteps []mutation.StartingStep
	for index, startingStep := range req.StartingSteps {
		input := p.Value{Type: p.ValueTypeNull}
		if index < len(convertedInputs) {
			input = convertedInputs[index]
		}
		startingSteps = append(startingSteps, mutation.StartingStep{
			StepID:      startingStep.StepId,
			Input:       input,
			SkipWaitFor: startingStep.SkipWaitFor,
		})
	}

	for attempt := 0; ; attempt++ {
		err := e.tryStartRun(ctx, shardID, req, startingSteps, uploader)
		if err == nil {
			return nil
		}
		if !shouldRetry(err, attempt, e.cfg.MaxTransientErrorRetries) {
			return err
		}
	}
}

func (e *runEngineImpl) tryStartRun(ctx context.Context, shardID int32, req *pb.StartRunRequest,
	startingSteps []mutation.StartingStep, conv blobs.Uploader) errors.CategorizedError {

	now := time.Now()
	run := &p.RunRow{
		ShardID:                   shardID,
		RowType:                   p.RowTypeRun,
		Namespace:                 req.Namespace,
		ID:                        req.RunId,
		FlowType:                  req.FlowType,
		TaskListName:              req.TaskListName,
		Status:                    p.RunStatusPending,
		Version:                   0,
		StateMap:                  make(map[string]p.Value),
		UnconsumedChannelMessages: make(map[string][]p.ChannelMessage),
		StepExeIDCounters:         make(map[string]int32),
		ActiveStepExecutions:      make(map[string]p.ActiveStepExecution),
		CreatedAt:                 now,
		UpdatedAt:                 now,
	}

	runMutation := e.mutations.NewMutationForCreate(shardID, run, now)
	runMutation.SpawnStartingSteps(startingSteps)
	runMutation.EnqueueInitialDispatchTask()
	runMutation.UpdateVisibility(p.RunStatusPending)
	runMutation.AddHistoryRunStart(req)
	return runMutation.Commit(ctx, conv)
}

// ============================================================================
// StopRun
// ============================================================================

func (e *runEngineImpl) StopRun(ctx context.Context, namespace, runID string, stopDecision pb.StopDecision, reason string) (bool, string, string, errors.CategorizedError) {
	e.logger.Debug("RunEngine.StopRun", tag.RunID(runID), tag.Namespace(namespace))
	shardID := e.shardMapper.GetShardID(namespace, runID)

	for attempt := 0; ; attempt++ {
		wasActive, taskListName, workerID, err := e.tryStopRun(ctx, shardID, namespace, runID, stopDecision, reason)
		if err == nil {
			return wasActive, taskListName, workerID, nil
		}
		if !shouldRetry(err, attempt, e.cfg.MaxTransientErrorRetries) {
			return false, "", "", err
		}
	}
}

func terminalStatusFromStopDecision(stopDecision pb.StopDecision) (p.RunStatus, errors.CategorizedError) {
	switch stopDecision {
	case pb.StopDecision_STOP_DECISION_COMPLETE:
		return p.RunStatusCompleted, nil
	case pb.StopDecision_STOP_DECISION_FAIL:
		return p.RunStatusFailed, nil
	default:
		return 0, errors.NewInvalidInputError("stop_decision must be COMPLETE or FAIL", nil)
	}
}

func (e *runEngineImpl) tryStopRun(ctx context.Context, shardID int32, namespace, runID string, stopDecision pb.StopDecision, reason string) (bool, string, string, errors.CategorizedError) {
	terminalStatus, err := terminalStatusFromStopDecision(stopDecision)
	if err != nil {
		return false, "", "", err
	}
	if len(reason) > e.cfg.StepRetryLastErrorMaxBytes {
		return false, "", "", errors.NewInvalidInputError(
			fmt.Sprintf("reason exceeds max length (%d bytes)", e.cfg.StepRetryLastErrorMaxBytes), nil)
	}
	runMutation, err := e.mutations.NewMutationForUpdate(ctx, shardID, namespace, runID)
	if err != nil {
		return false, "", "", err
	}
	run := runMutation.GetRun()
	if run.Status.IsTerminal() {
		e.logger.Debug("StopRun no-op (already terminal)",
			tag.Shard(shardID), tag.Namespace(namespace), tag.RunID(runID),
			tag.ToStatus(run.Status.Name()))
		return false, run.TaskListName, run.WorkerID, nil
	}

	runMutation.TransitionToTerminal(terminalStatus, mutation.TransitionReasonStopRun)
	runMutation.UpdateVisibility(terminalStatus)
	runMutation.AddHistoryRunStop(terminalStatus, reason)
	if commitErr := runMutation.Commit(ctx, nil); commitErr != nil {
		return false, "", "", commitErr
	}
	return true, run.TaskListName, run.WorkerID, nil
}

// ============================================================================
// ProcessStepExecuteCompleted
// ============================================================================

func (e *runEngineImpl) ProcessStepExecuteCompleted(ctx context.Context, shardID int32, namespace string, req *pb.StepExecuteCompletedRequest) (*pb.WorkerCallResponse, errors.CategorizedError) {
	e.logger.Debug("RunEngine.ProcessStepExecuteCompleted", tag.RunID(req.RunId), tag.Namespace(namespace), tag.Shard(shardID), tag.StepExeID(req.StepExeId))

	uploader := e.blobs.NewUploader(shardID, req.Namespace, req.RunId)
	stateMap := uploader.Map(req.StateToUpsert)
	channelPubs := uploader.ChannelPublish(req.ChannelPublish)
	var nextSteps []mutation.NextStep
	for _, nextStep := range req.NextSteps {
		nextSteps = append(nextSteps, mutation.NextStep{
			StepID:             nextStep.StepId,
			Input:              uploader.Single(nextStep.Input),
			SkipWaitFor:        nextStep.SkipWaitFor,
			WaitForMethodExeID: nextStep.WaitForMethodExeId,
			ExecuteMethodExeID: nextStep.ExecuteMethodExeId,
		})
	}

	for attempt := 0; ; attempt++ {
		wcr, err := e.tryProcessStepExecuteCompleted(ctx, shardID, namespace, req, uploader, stateMap, nextSteps, channelPubs)
		if err == nil {
			return wcr, nil
		}
		if !shouldRetry(err, attempt, e.cfg.MaxTransientErrorRetries) {
			return nil, err
		}
	}
}

func (e *runEngineImpl) tryProcessStepExecuteCompleted(
	ctx context.Context, shardID int32, namespace string, req *pb.StepExecuteCompletedRequest,
	uploader blobs.Uploader,
	stateMap map[string]p.Value, nextSteps []mutation.NextStep,
	channelPubs map[string][]p.ChannelMessage,
) (*pb.WorkerCallResponse, errors.CategorizedError) {
	runMutation, duplicateReq, err := e.mutations.NewMutationForUpdateWithWorkerContext(ctx, shardID, namespace, req.RunId, req.Context)
	if err != nil {
		return nil, err
	}
	if duplicateReq {
		return e.buildWorkerCallResponseWithCatchUp(ctx, runMutation.GetRun(), req.Context.GetLastReceivedExternalChannelMessageId())
	}

	if hasStepMethodOutcomeFailed(req.GetExecuteMethod()) {
		if req.StopDecision == pb.StopDecision_STOP_DECISION_FAIL {
			// terminal failure — unchanged
		} else if len(req.NextSteps) > 0 && req.StopDecision == pb.StopDecision_STOP_DECISION_NONE {
			// retry-exhaust proceed — run continues via next_steps
		} else {
			return nil, errors.NewInvalidInputError("execute_method FAILED requires stop_decision FAIL or next_steps with NONE", nil)
		}
	}

	if err := runMutation.RecordWorkerContext(req.Context); err != nil {
		return nil, err
	}
	runMutation.UpsertStateMap(stateMap)
	runMutation.SpliceChannelsOnExecuteAndPublishInternalChannels(req, channelPubs)

	var fromStepExeID string
	var conditionResults []*pb.ConditionResult
	if existing, ok := runMutation.GetRun().ActiveStepExecutions[req.StepExeId]; ok {
		fromStepExeID = existing.FromStepExeID
		if len(existing.ConditionResults) > 0 {
			conditionResults = pbconv.PersistenceConditionResultsToPb(existing.ConditionResults)
		}
	}

	cancelIDs := append([]string{req.StepExeId}, req.CanceledStepExecutions...)
	for _, cancelID := range req.CanceledStepExecutions {
		if cancelID == req.StepExeId {
			continue
		}
		if _, ok := runMutation.GetRun().ActiveStepExecutions[cancelID]; !ok {
			e.logger.Debug("Cancellation target already absent",
				tag.Shard(shardID), tag.Namespace(namespace),
				tag.RunID(req.RunId), tag.StepExeID(cancelID))
		}
	}
	runMutation.RemoveSteps(cancelIDs...)
	runMutation.SpawnNextSteps(req.StepExeId, nextSteps)
	runMutation.PromoteReportedUnblocks(req.StepsUnblocked)
	runMutation.TransitionToStatusFromStopDecision(req.StopDecision, mutation.TransitionReasonStepExecuteCompleted)

	runMutation.AddHistoryStepExecuteCompleted(req, fromStepExeID, conditionResults, "")
	runMutation.UpdateVisibilityIfStatusChanged()
	runMutation.AddHistoryRunStopIfTerminal()

	if err := runMutation.Commit(ctx, uploader); err != nil {
		return nil, err
	}
	return e.buildWorkerCallResponseWithCatchUp(ctx, runMutation.GetRun(), req.GetContext().GetLastReceivedExternalChannelMessageId())
}

// ============================================================================
// ProcessStepWaitForCompleted
// ============================================================================

func (e *runEngineImpl) ProcessStepWaitForCompleted(ctx context.Context, shardID int32, namespace string, req *pb.StepWaitForCompletedRequest) (*pb.WorkerCallResponse, errors.CategorizedError) {
	e.logger.Debug("RunEngine.ProcessStepWaitForCompleted", tag.RunID(req.RunId), tag.Namespace(namespace), tag.Shard(shardID), tag.StepExeID(req.StepExeId))

	hasMethodFailed := hasStepMethodOutcomeFailed(req.GetWaitForMethod())
	var waitCond p.WaitForCondition
	if !hasMethodFailed {
		waitCond = pbconv.PbWaitForConditionToPersistence(req.WaitForCondition)
		if err := waitCond.Validate(); err != nil {
			return nil, err
		}
	}

	uploader := e.blobs.NewUploader(shardID, req.Namespace, req.RunId)
	stateMap := uploader.Map(req.StateToUpsert)

	channelPubs := uploader.ChannelPublish(req.ChannelPublish)

	var nextSteps []mutation.NextStep
	if hasMethodFailed {
		for _, nextStep := range req.NextSteps {
			nextSteps = append(nextSteps, mutation.NextStep{
				StepID:             nextStep.StepId,
				Input:              uploader.Single(nextStep.Input),
				SkipWaitFor:        nextStep.SkipWaitFor,
				WaitForMethodExeID: nextStep.WaitForMethodExeId,
				ExecuteMethodExeID: nextStep.ExecuteMethodExeId,
			})
		}
	}

	for attempt := 0; ; attempt++ {
		wcr, err := e.tryProcessStepWaitForCompleted(ctx, shardID, namespace, req, uploader, stateMap, waitCond, channelPubs, nextSteps)
		if err == nil {
			return wcr, nil
		}
		if !shouldRetry(err, attempt, e.cfg.MaxTransientErrorRetries) {
			return nil, err
		}
	}
}

func (e *runEngineImpl) tryProcessStepWaitForCompleted(
	ctx context.Context, shardID int32, namespace string, req *pb.StepWaitForCompletedRequest,
	uploader blobs.Uploader,
	stateMap map[string]p.Value, waitCond p.WaitForCondition, channelPubs map[string][]p.ChannelMessage,
	nextSteps []mutation.NextStep,
) (*pb.WorkerCallResponse, errors.CategorizedError) {
	runMutation, duplicateReq, err := e.mutations.NewMutationForUpdateWithWorkerContext(ctx, shardID, namespace, req.RunId, req.Context)
	if err != nil {
		return nil, err
	}
	if duplicateReq {
		return e.buildWorkerCallResponseWithCatchUp(ctx, runMutation.GetRun(), req.Context.GetLastReceivedExternalChannelMessageId())
	}

	if hasStepMethodOutcomeFailed(req.GetWaitForMethod()) {
		if len(nextSteps) > 0 {
			return e.tryProcessStepWaitForFailedAndProceed(ctx, runMutation, req, uploader, stateMap, nextSteps)
		}
		return e.tryProcessStepWaitForFailed(ctx, runMutation, req, uploader, stateMap)
	}

	if err := runMutation.RecordWorkerContext(req.Context); err != nil {
		return nil, err
	}
	runMutation.UpsertStateMap(stateMap)
	runMutation.TransitionStepToWaitingForCondition(req.StepExeId, waitCond)
	runMutation.PublishInternalChannels(channelPubs)
	runMutation.PromoteReportedUnblocks(req.StepsUnblocked)

	fromStepExeID := ""
	if existing, ok := runMutation.GetRun().ActiveStepExecutions[req.StepExeId]; ok {
		fromStepExeID = existing.FromStepExeID
	}
	runMutation.AddHistoryStepWaitForCompleted(req, fromStepExeID, "")

	if err := runMutation.Commit(ctx, uploader); err != nil {
		return nil, err
	}
	return e.buildWorkerCallResponseWithCatchUp(ctx, runMutation.GetRun(), req.GetContext().GetLastReceivedExternalChannelMessageId())
}

func (e *runEngineImpl) tryProcessStepWaitForFailed(
	ctx context.Context, runMutation mutation.RunMutation, req *pb.StepWaitForCompletedRequest,
	conv blobs.Uploader, stateMap map[string]p.Value,
) (*pb.WorkerCallResponse, errors.CategorizedError) {
	if err := runMutation.RecordWorkerContext(req.Context); err != nil {
		return nil, err
	}
	runMutation.UpsertStateMap(stateMap)
	fromStepExeID := ""
	if existing, ok := runMutation.GetRun().ActiveStepExecutions[req.StepExeId]; ok {
		fromStepExeID = existing.FromStepExeID
	}
	runMutation.RemoveSteps(req.StepExeId)
	runMutation.TransitionToTerminal(p.RunStatusFailed, mutation.TransitionReasonStepWaitForMethodFailed)
	runMutation.AddHistoryStepWaitForCompleted(req, fromStepExeID, "")
	runMutation.UpdateVisibility(p.RunStatusFailed)
	runMutation.AddHistoryRunStop(p.RunStatusFailed, "")

	if err := runMutation.Commit(ctx, conv); err != nil {
		return nil, err
	}
	wcr, wcrErr := e.buildWorkerCallResponseWithCatchUp(ctx, runMutation.GetRun(), req.GetContext().GetLastReceivedExternalChannelMessageId())
	if wcrErr != nil {
		return nil, wcrErr
	}
	return wcr, nil
}

func (e *runEngineImpl) tryProcessStepWaitForFailedAndProceed(
	ctx context.Context, runMutation mutation.RunMutation, req *pb.StepWaitForCompletedRequest,
	uploader blobs.Uploader, stateMap map[string]p.Value, nextSteps []mutation.NextStep,
) (*pb.WorkerCallResponse, errors.CategorizedError) {
	if err := runMutation.RecordWorkerContext(req.Context); err != nil {
		return nil, err
	}
	runMutation.UpsertStateMap(stateMap)
	fromStepExeID := ""
	if existing, ok := runMutation.GetRun().ActiveStepExecutions[req.StepExeId]; ok {
		fromStepExeID = existing.FromStepExeID
	}
	runMutation.RemoveSteps(req.StepExeId)
	runMutation.SpawnNextSteps(req.StepExeId, nextSteps)
	runMutation.AddHistoryStepWaitForCompleted(req, fromStepExeID, "")

	if err := runMutation.Commit(ctx, uploader); err != nil {
		return nil, err
	}
	e.logger.Debug("Step method failed, run continued via next_steps",
		tag.Namespace(runMutation.GetRun().Namespace), tag.RunID(req.RunId), tag.StepExeID(req.StepExeId),
		tag.Value("wait_for"))
	return e.buildWorkerCallResponseWithCatchUp(ctx, runMutation.GetRun(), req.GetContext().GetLastReceivedExternalChannelMessageId())
}

// ============================================================================
// ProcessStepsUnblocked
// ============================================================================

// ProcessStepsUnblocked durably checkpoints a worker-driven sibling unblock
// triggered out of band of any step completion (external channel delivery
// or local-timer fire while status=Running). See the RunEngine interface
// doc and docs/wait-for-conditions-design.md for the correctness argument.
func (e *runEngineImpl) ProcessStepsUnblocked(ctx context.Context, shardID int32, req *pb.StepsUnblockedRequest) (*pb.WorkerCallResponse, errors.CategorizedError) {
	e.logger.Debug("RunEngine.ProcessStepsUnblocked", tag.RunID(req.RunId), tag.Namespace(req.Namespace), tag.Shard(shardID), tag.UnblockedCount(len(req.StepsUnblocked)))
	for attempt := 0; ; attempt++ {
		wcr, err := e.tryProcessStepsUnblocked(ctx, shardID, req)
		if err == nil {
			return wcr, nil
		}
		if !shouldRetry(err, attempt, e.cfg.MaxTransientErrorRetries) {
			return nil, err
		}
	}
}

func (e *runEngineImpl) tryProcessStepsUnblocked(ctx context.Context, shardID int32, req *pb.StepsUnblockedRequest) (*pb.WorkerCallResponse, errors.CategorizedError) {
	if len(req.StepsUnblocked) == 0 {
		return nil, errors.NewInvalidInputError("ProcessStepsUnblocked: no steps unblocked", nil)
	}

	runMutation, duplicateReq, err := e.mutations.NewMutationForUpdateWithWorkerContext(ctx, shardID, req.Namespace, req.RunId, req.Context)
	if err != nil {
		return nil, err
	}
	if duplicateReq {
		return e.buildWorkerCallResponseWithCatchUp(ctx, runMutation.GetRun(), req.Context.GetLastReceivedExternalChannelMessageId())
	}

	if err := runMutation.RecordWorkerContext(req.Context); err != nil {
		return nil, err
	}
	runMutation.PromoteReportedUnblocks(req.StepsUnblocked)
	runMutation.AddHistoryStepsUnblocked(req, "")

	if err := runMutation.Commit(ctx, nil); err != nil {
		return nil, err
	}
	return e.buildWorkerCallResponseWithCatchUp(ctx, runMutation.GetRun(), req.GetContext().GetLastReceivedExternalChannelMessageId())
}

// ============================================================================
// PublishExternalChannelMessages
// ============================================================================

func (e *runEngineImpl) PublishExternalChannelMessages(ctx context.Context, shardID int32, req *pb.PublishToChannelRequest) errors.CategorizedError {
	e.logger.Debug("RunEngine.PublishExternalChannelMessages", tag.RunID(req.RunId), tag.Namespace(req.Namespace), tag.Shard(shardID), tag.ChannelName(req.ChannelName))
	// No GetCappedContext: PublishToChannel is an API call, any instance handles it.
	// Use the caller's request context for timeout.

	// Convert values through BlobStore once (idempotent, safe to reuse across retries).
	// Side effect: req.Values' EncodedObjects are mutated to BlobIdInternalOnly,
	// so the subsequent addHistoryChannelPublish embeds storage-form Values.
	uploader := e.blobs.NewUploader(shardID, req.Namespace, req.RunId)
	pVals := uploader.Slice(req.Values)

	// Channel publishes are a fan-in contention point: many cross-run
	// publishers (e.g. N sub-agents replying to one parent) plus the run's
	// own step completions all CAS the same run row. We do a small in-process
	// optimistic retry here; the durable backoff-and-retry of a CAS conflict
	// (Aborted) lives on the CLIENT (see RawClient.PublishToChannel), which
	// re-sends the whole request with jittered backoff so contenders
	// desynchronize. Each tryProcessExternalChannelMessages re-reads the run
	// and reuses the already-staged pVals, so retries are safe.
	for attempt := 0; ; attempt++ {
		err := e.tryProcessExternalChannelMessages(ctx, shardID, req, pVals, uploader)
		if err == nil {
			return nil
		}
		if !shouldRetry(err, attempt, e.cfg.MaxTransientErrorRetries) {
			return err
		}
	}
}

func (e *runEngineImpl) tryProcessExternalChannelMessages(ctx context.Context, shardID int32, req *pb.PublishToChannelRequest, pVals []p.Value, uploader blobs.Uploader) errors.CategorizedError {
	runMutation, err := e.mutations.NewMutationForUpdate(ctx, shardID, req.Namespace, req.RunId)
	if err != nil {
		return err
	}
	run := runMutation.GetRun()
	if run.Status.IsTerminal() {
		return errors.NewInvalidInputError(fmt.Sprintf("run is in terminal status %d, cannot publish to channel", run.Status), nil)
	}

	baseID := run.ExternalChannelMessageCounter
	messages := make([]p.ChannelMessage, len(pVals))
	for index, value := range pVals {
		messages[index] = p.ChannelMessage{ID: baseID + int64(index) + 1, Value: value}
	}
	runMutation.PublishExternalChannels(req.ChannelName, messages)

	switch run.Status {
	case p.RunStatusAllStepsWaitingForConditions, p.RunStatusPending:
		if _, evalErr := runMutation.MaybeTransitionToPendingIfPromoteWaitingSteps(time.Now().UnixMilli(), mutation.TransitionReasonExternalChannelReceived); evalErr != nil {
			return evalErr
		}
	case p.RunStatusRunning:
		// Best-effort delivery via extEventDispatcher is handled by the
		// caller (api layer) after this function returns.
	}

	runMutation.AddHistoryChannelPublish(req)
	runMutation.UpdateVisibilityIfStatusChanged()
	return runMutation.Commit(ctx, uploader)
}

// ============================================================================
// HandleHeartbeatTimeout
// ============================================================================

func (e *runEngineImpl) HandleHeartbeatTimeout(ctx context.Context, shardID int32, req *HeartbeatTimerFiredRequest) errors.CategorizedError {
	e.logger.Debug("RunEngine.HandleHeartbeatTimeout", tag.RunID(req.RunID), tag.Namespace(req.Namespace), tag.Shard(shardID), tag.TaskID(req.TimerID.String()))
	ctx, cancel := e.shardManager.GetCappedContext(ctx, shardID)
	defer cancel()

	for attempt := 0; ; attempt++ {
		err := e.tryProcessHeartbeatTimeout(ctx, shardID, req)
		if err == nil {
			return nil
		}
		if !shouldRetry(err, attempt, e.cfg.MaxTransientErrorRetries) {
			return err
		}
	}
}

func (e *runEngineImpl) tryProcessHeartbeatTimeout(ctx context.Context, shardID int32, req *HeartbeatTimerFiredRequest) errors.CategorizedError {
	runMutation, err := e.mutations.NewMutationForUpdate(ctx, shardID, req.Namespace, req.RunID)
	if err != nil {
		if err.IsNotFoundError() {
			e.logger.Warn("Heartbeat timer fired for non-existent run, treating as stale",
				tag.RunID(req.RunID), tag.Namespace(req.Namespace), tag.Shard(shardID))
			return nil
		}
		return err
	}
	run := runMutation.GetRun()

	if run.Status != p.RunStatusRunning {
		e.logger.Info("heartbeat timer skipped, run not running",
			tag.Shard(shardID), tag.Namespace(req.Namespace), tag.RunID(req.RunID),
			tag.StatusName(run.Status.Name()))
		return nil
	}
	if run.HeartbeatTimerID != req.TimerID {
		return nil
	}

	e.logger.Warn("Heartbeat timer fired, worker timeout",
		tag.RunID(req.RunID), tag.Namespace(req.Namespace), tag.Shard(shardID),
		tag.WorkerID(run.WorkerID))
	runMutation.TransitionToWaitingForWorker(mutation.TransitionReasonHeartbeatTimeout)
	return runMutation.Commit(ctx, nil)
}

// ============================================================================
// HandleStepWaitForTimerFired
// ============================================================================

func (e *runEngineImpl) HandleStepWaitForTimerFired(ctx context.Context, shardID int32, req *StepWaitForTimerFiredRequest) errors.CategorizedError {
	e.logger.Debug("RunEngine.HandleStepWaitForTimerFired", tag.RunID(req.RunID), tag.Namespace(req.Namespace), tag.Shard(shardID), tag.TaskID(req.TimerID.String()))
	ctx, cancel := e.shardManager.GetCappedContext(ctx, shardID)
	defer cancel()

	for attempt := 0; ; attempt++ {
		err := e.tryProcessStepWaitForTimerFired(ctx, shardID, req)
		if err == nil {
			return nil
		}
		if !shouldRetry(err, attempt, e.cfg.MaxTransientErrorRetries) {
			return err
		}
	}
}

func (e *runEngineImpl) tryProcessStepWaitForTimerFired(ctx context.Context, shardID int32, req *StepWaitForTimerFiredRequest) errors.CategorizedError {
	runMutation, err := e.mutations.NewMutationForUpdate(ctx, shardID, req.Namespace, req.RunID)
	if err != nil {
		if err.IsNotFoundError() {
			e.logger.Warn("Step wait-for timer fired for non-existent run, treating as stale",
				tag.RunID(req.RunID), tag.Namespace(req.Namespace), tag.Shard(shardID))
			return nil
		}
		return err
	}
	run := runMutation.GetRun()

	if run.Status != p.RunStatusAllStepsWaitingForConditions {
		metrics.CounterStepWaitForTimerFiredDroppedNotAllWaiting.Inc(metrics.TagNamespace(req.Namespace))
		return nil
	}
	if run.ActiveDurableTimerID != req.TimerID {
		return nil
	}

	if err := runMutation.MaybeTransitionToPendingOnDurableTimerFired(req.FireAtUnixMs, mutation.TransitionReasonStepWaitForTimerFired); err != nil {
		return err
	}
	runMutation.UpdateVisibilityIfStatusChanged()
	return runMutation.Commit(ctx, nil)
}

// ============================================================================
// GetRun
// ============================================================================

// GetRun reads the full run state. Returns nil if status doesn't match filter.
// Uses SecondaryPreferred for the underlying read because the visibility API
// is read-only and tolerant of replica lag; this offloads load from primary.
func (e *runEngineImpl) GetRun(ctx context.Context, namespace, runID string, statusFilter []p.RunStatus) (*pb.GetRunResponse, errors.CategorizedError) {
	e.logger.Debug("RunEngine.GetRun", tag.RunID(runID), tag.Namespace(namespace))
	shardID := e.shardMapper.GetShardID(namespace, runID)

	run, err := e.runStore.GetRun(ctx, shardID, namespace, runID, p.GetRunOptions{ReadPreference: p.ReadPrefSecondaryPreferred})
	if err != nil {
		if err.IsNotFoundError() {
			return &pb.GetRunResponse{Found: false}, nil
		}
		return nil, err
	}

	// Check status filter
	if len(statusFilter) > 0 {
		matched := false
		for _, s := range statusFilter {
			if run.Status == s {
				matched = true
				break
			}
		}
		if !matched {
			return &pb.GetRunResponse{Found: false, Status: int32(run.Status)}, nil
		}
	}

	// Batch-fetch all blob refs so we can resolve them to inline values
	resolver := e.blobs.NewResolver(run)
	if blobErr := resolver.LoadAllForRunRow(ctx); blobErr != nil {
		return nil, blobErr
	}

	resp := &pb.GetRunResponse{
		Found:                         true,
		RunId:                         run.ID,
		Namespace:                     run.Namespace,
		FlowType:                      run.FlowType,
		TaskListName:                  run.TaskListName,
		Status:                        int32(run.Status),
		WorkerRequestCounter:          run.WorkerRequestCounter,
		Version:                       run.Version,
		ServerTimestampMs:             time.Now().UnixMilli(),
		ExternalChannelMessageCounter: run.ExternalChannelMessageCounter,
	}

	if len(run.StepExeIDCounters) > 0 {
		resp.StepExecutionIdCounters = make(map[string]int32, len(run.StepExeIDCounters))
		for k, v := range run.StepExeIDCounters {
			resp.StepExecutionIdCounters[k] = v
		}
	}

	resp.State = resolver.ResolveStateMap()
	resp.UnconsumedChannelMessages = resolver.ResolveUnconsumedChannelMessages()
	resp.ActiveStepExecutions = resolver.ResolveActiveStepExecutions()

	return resp, nil
}

// ============================================================================
// HandleRunDispatchResult
// ============================================================================

// HandleRunDispatchResult transitions a run after DispatchRun completes.
// transitionToRunning=true (sync match): Running + heartbeat timer + WorkerID.
// transitionToRunning=false (async match miss): WaitingForWorker.
//
// On sync match success, returns a PollForRunResponse that the matching service
// delivers to the worker. On async miss, returns nil.
//
// Reads the run internally for CAS. Idempotent: if the run is already at or
// past the target state, the call is a no-op. Returns CAS errors directly
// so the task processor retries the dispatch task.
func (e *runEngineImpl) HandleRunDispatchResult(ctx context.Context, shardID int32, namespace, runID string, transitionToRunning bool, workerID string) (*pb.PollForRunResponse, errors.CategorizedError) {
	e.logger.Debug("RunEngine.HandleRunDispatchResult", tag.RunID(runID), tag.Namespace(namespace), tag.Shard(shardID), tag.WorkerID(workerID), tag.Value(transitionToRunning))
	maxRetries := e.cfg.MaxTransientErrorRetries

	for attempt := 0; attempt <= maxRetries; attempt++ {
		runMutation, err := e.mutations.NewMutationForUpdate(ctx, shardID, namespace, runID)
		if err != nil {
			if err.IsNotFoundError() {
				e.logger.Warn("HandleRunDispatchResult: run not found, treating dispatch as stale",
					tag.RunID(runID), tag.Namespace(namespace), tag.Shard(shardID))
				return nil, nil
			}
			e.logger.Warn("HandleRunDispatchResult: GetRun failed",
				tag.RunID(runID), tag.Error(err))
			continue
		}
		run := runMutation.GetRun()

		if run.Status != p.RunStatusPending && run.Status != p.RunStatusWaitingForWorker {
			e.logger.Info("HandleRunDispatchResult: skipping dispatch, run already progressed",
				tag.RunID(runID), tag.Namespace(namespace), tag.Shard(shardID),
				tag.StatusName(run.Status.Name()))
			return nil, nil
		}

		if transitionToRunning {
			runMutation.TransitionToRunning(workerID, e.cfg.HeartbeatTimerDuration, mutation.TransitionReasonHandleRunDispatchResult)
		} else {
			runMutation.TransitionToWaitingForWorker(mutation.TransitionReasonHandleRunDispatchResult)
		}

		if err := runMutation.Commit(ctx, nil); err != nil {
			e.logger.Warn("HandleRunDispatchResult: Commit failed",
				tag.RunID(runID), tag.Error(err))
			continue
		}

		if transitionToRunning {
			return e.buildPollForRunResponse(ctx, run)
		}
		return nil, nil
	}

	e.logger.Warn("HandleRunDispatchResult: retries exhausted",
		tag.RunID(runID), tag.Namespace(namespace), tag.MaxRetries(maxRetries))
	return nil, errors.NewCASError("HandleRunDispatchResult: retries exhausted", nil)
}

// buildPollForRunResponse constructs the PollForRunResponse that matching
// delivers to the worker when a run starts (sync match or async pickup).
func (e *runEngineImpl) buildPollForRunResponse(ctx context.Context, run *p.RunRow) (*pb.PollForRunResponse, errors.CategorizedError) {
	resolver := e.blobs.NewResolver(run)
	if blobErr := resolver.LoadAllForRunRow(ctx); blobErr != nil {
		return nil, blobErr
	}

	effectiveNow := time.Now().UnixMilli()
	if run.DurableTimerFireAt > effectiveNow {
		// this is to prevent timer skew in distributed systems.
		// in case of durable timer fired, worker needs the time to not go backward
		// so that it can wake up the steps
		effectiveNow = run.DurableTimerFireAt
	}

	resp := &pb.PollForRunResponse{
		RunId:                         run.ID,
		Namespace:                     run.Namespace,
		FlowType:                      run.FlowType,
		WorkerId:                      run.WorkerID,
		WorkerRequestCounter:          run.WorkerRequestCounter,
		ExternalChannelMessageCounter: run.ExternalChannelMessageCounter,
		ServerTimestampMs:             effectiveNow,
		StepMethodExeCounter:          run.StepMethodExeCounter,
	}
	if len(run.StepExeIDCounters) > 0 {
		resp.StepExecutionIdCounters = make(map[string]int32, len(run.StepExeIDCounters))
		for k, v := range run.StepExeIDCounters {
			resp.StepExecutionIdCounters[k] = v
		}
	}
	resp.State = resolver.ResolveStateMap()
	resp.UnconsumedChannelMessages = resolver.ResolveUnconsumedChannelMessages()
	resp.ActiveStepExecutions = resolver.ResolveActiveStepExecutions()
	return resp, nil
}

// ============================================================================
// HandleBatchAsyncMatch
// ============================================================================

// AsyncMatchRequest identifies a single run to transition for async match pickup.
type AsyncMatchRequest struct {
	ShardID   int32
	Namespace string
	RunID     string
}

// HandleBatchAsyncMatch transitions multiple runs to Running for async match
// pickup. Each run is processed independently with an internal CAS retry loop
// (up to cfg.MaxCASRetries attempts). Returns a map of run_id → success.
//
// success=true: safe to delete from the async match queue. Includes:
//   - Run transitioned to Running successfully
//   - Run not found (stale dispatch)
//   - Run already past WaitingForWorker (stale dispatch)
//
// success=false: keep record in queue for next cycle. Includes:
//   - Transient store error
//   - CAS failure after all retries
func (e *runEngineImpl) HandleBatchAsyncMatch(ctx context.Context, requests []AsyncMatchRequest) map[string]bool {
	e.logger.Debug("RunEngine.HandleBatchAsyncMatch", tag.Count(len(requests)))
	results := make(map[string]bool, len(requests))
	for _, req := range requests {
		results[req.RunID] = e.processOneAsyncMatch(ctx, req)
	}
	return results
}

func (e *runEngineImpl) processOneAsyncMatch(ctx context.Context, req AsyncMatchRequest) bool {
	maxRetries := e.cfg.MaxTransientErrorRetries

	for attempt := 0; attempt <= maxRetries; attempt++ {
		runMutation, err := e.mutations.NewMutationForUpdate(ctx, req.ShardID, req.Namespace, req.RunID)
		if err != nil {
			if err.IsNotFoundError() {
				e.logger.Warn("HandleBatchAsyncMatch: run not found, treating as stale",
					tag.RunID(req.RunID), tag.Namespace(req.Namespace), tag.Shard(req.ShardID))
				return true
			}
			e.logger.Warn("HandleBatchAsyncMatch: GetRun failed",
				tag.RunID(req.RunID), tag.Error(err))
			continue
		}
		run := runMutation.GetRun()

		if run.Status != p.RunStatusPending && run.Status != p.RunStatusWaitingForWorker {
			return true
		}

		runMutation.TransitionToRunning("", 30*time.Second, mutation.TransitionReasonBatchAsyncMatch)

		if err := runMutation.Commit(ctx, nil); err != nil {
			e.logger.Warn("HandleBatchAsyncMatch: Commit failed",
				tag.RunID(req.RunID), tag.Error(err))
			continue
		}
		return true
	}

	e.logger.Warn("HandleBatchAsyncMatch: retries exhausted",
		tag.RunID(req.RunID), tag.Namespace(req.Namespace), tag.MaxRetries(maxRetries))
	return false
}

// ============================================================================
// ProcessRecordHeartbeat
// ============================================================================

func (e *runEngineImpl) ProcessRecordHeartbeat(ctx context.Context, shardID int32, req *pb.ProcessRecordHeartbeatRequest) (*pb.WorkerCallResponse, errors.CategorizedError) {
	e.logger.Debug("RunEngine.ProcessRecordHeartbeat", tag.RunID(req.RunId), tag.Namespace(req.Namespace), tag.Shard(shardID), tag.WorkerID(req.GetContext().GetWorkerId()))
	maxRetries := e.cfg.MaxTransientErrorRetries
	for attempt := 0; attempt <= maxRetries; attempt++ {
		runMutation, duplicateReq, err := e.mutations.NewMutationForUpdateWithWorkerContext(ctx, shardID, req.Namespace, req.RunId, req.Context)
		if err != nil {
			return nil, err
		}
		if duplicateReq {
			return e.buildWorkerCallResponseWithCatchUp(ctx, runMutation.GetRun(), req.Context.GetLastReceivedExternalChannelMessageId())
		}

		if err := runMutation.RecordWorkerContext(req.Context); err != nil {
			e.logger.Debug("worker retry state rejected", tag.RunID(req.RunId), tag.Namespace(req.Namespace), tag.Error(err))
			return nil, err
		}
		runMutation.RenewHeartbeatTimer()

		if commitErr := runMutation.Commit(ctx, nil); commitErr != nil {
			if commitErr.IsCASError() && attempt < maxRetries {
				continue
			}
			return nil, commitErr
		}
		return e.buildWorkerCallResponseWithCatchUp(ctx, runMutation.GetRun(), req.Context.GetLastReceivedExternalChannelMessageId())
	}
	return nil, errors.NewCASError("ProcessRecordHeartbeat: retries exhausted", nil)
}

// ============================================================================
// ProcessReleaseRun
// ============================================================================

func (e *runEngineImpl) ProcessReleaseRun(ctx context.Context, shardID int32, req *pb.ProcessReleaseRunRequest) (*pb.ProcessReleaseRunResponse, errors.CategorizedError) {
	e.logger.Debug("RunEngine.ProcessReleaseRun", tag.RunID(req.RunId), tag.Namespace(req.Namespace), tag.Shard(shardID))
	switch req.GetReleaseReason() {
	case pb.ReleaseRunReason_RELEASE_RUN_REASON_YIELD_TO_ANOTHER_WORKER:
		return e.processReleaseRunYield(ctx, shardID, req)
	case pb.ReleaseRunReason_RELEASE_RUN_REASON_ALL_STEPS_WAITING:
		return e.processReleaseRunAllStepsWaiting(ctx, shardID, req)
	default:
		return nil, errors.NewInvalidInputError("release_reason is required", nil)
	}
}

func (e *runEngineImpl) processReleaseRunYield(ctx context.Context, shardID int32, req *pb.ProcessReleaseRunRequest) (*pb.ProcessReleaseRunResponse, errors.CategorizedError) {
	maxRetries := e.cfg.MaxTransientErrorRetries
	for attempt := 0; attempt <= maxRetries; attempt++ {
		runMutation, err := e.mutations.NewMutationForUpdate(ctx, shardID, req.Namespace, req.RunId)
		if err != nil {
			if err.IsNotFoundError() {
				return &pb.ProcessReleaseRunResponse{}, nil
			}
			return nil, err
		}
		run := runMutation.GetRun()

		if run.WorkerID != req.WorkerId || run.Status != p.RunStatusRunning {
			e.logger.Debug("ProcessReleaseRun: yield skip (not owned or not running)",
				tag.RunID(req.RunId), tag.Namespace(req.Namespace),
				tag.WorkerID(req.WorkerId), tag.StatusName(run.Status.Name()))
			return &pb.ProcessReleaseRunResponse{}, nil
		}

		runMutation.TransitionToWaitingForWorker(mutation.TransitionReasonReleaseRunYield)
		if updErr := runMutation.Commit(ctx, nil); updErr != nil {
			if updErr.IsCASError() && attempt < maxRetries {
				continue
			}
			return nil, updErr
		}
		return &pb.ProcessReleaseRunResponse{}, nil
	}
	return nil, errors.NewCASError("ProcessReleaseRun: yield retries exhausted", nil)
}

func (e *runEngineImpl) processReleaseRunAllStepsWaiting(ctx context.Context, shardID int32, req *pb.ProcessReleaseRunRequest) (*pb.ProcessReleaseRunResponse, errors.CategorizedError) {
	maxRetries := e.cfg.MaxTransientErrorRetries
	for attempt := 0; attempt <= maxRetries; attempt++ {
		runMutation, duplicateReq, err := e.mutations.NewMutationForUpdateWithWorkerContext(ctx, shardID, req.Namespace, req.RunId, req.Context)
		if err != nil {
			return nil, err
		}
		if duplicateReq {
			wcr, wcrErr := e.buildWorkerCallResponseWithCatchUp(ctx, runMutation.GetRun(), req.Context.GetLastReceivedExternalChannelMessageId())
			if wcrErr != nil {
				return nil, wcrErr
			}
			return &pb.ProcessReleaseRunResponse{WorkerCallResponse: wcr}, nil
		}

		lastReceivedID := req.Context.GetLastReceivedExternalChannelMessageId()
		if err := runMutation.RecordWorkerContext(req.Context); err != nil {
			return nil, err
		}

		parked := false
		if runMutation.IsChannelCatchUpComplete(lastReceivedID) {
			runMutation.TransitionToAllStepsWaitingForConditions(mutation.TransitionReasonReleaseRunAllStepsWaiting)
			parked = true
		} else {
			e.logger.Debug("ProcessReleaseRun: worker behind on external messages",
				tag.RunID(req.RunId), tag.Namespace(req.Namespace))
		}

		if updErr := runMutation.Commit(ctx, nil); updErr != nil {
			if updErr.IsCASError() && attempt < maxRetries {
				continue
			}
			return nil, updErr
		}

		if parked {
			e.logger.Info("ProcessReleaseRun: parked all_steps_waiting",
				tag.RunID(req.RunId), tag.Namespace(req.Namespace))
		}

		wcr, wcrErr := e.buildWorkerCallResponseWithCatchUp(ctx, runMutation.GetRun(), lastReceivedID)
		if wcrErr != nil {
			return nil, wcrErr
		}
		return &pb.ProcessReleaseRunResponse{WorkerCallResponse: wcr}, nil
	}
	return nil, errors.NewCASError("ProcessReleaseRun: all_steps_waiting retries exhausted", nil)
}

// ============================================================================
// ForkRun
// ============================================================================

func (e *runEngineImpl) ForkRun(ctx context.Context, req *pb.ForkRunRequest) (string, errors.CategorizedError) {
	e.logger.Debug("RunEngine.ForkRun", tag.RunID(req.RunId), tag.Namespace(req.Namespace), tag.Value(req.ToEventId))
	shardID := e.shardMapper.GetShardID(req.Namespace, req.RunId)

	for attempt := 0; ; attempt++ {
		previousWorkerID, err := e.tryForkRun(ctx, shardID, req)
		if err == nil {
			return previousWorkerID, nil
		}
		if !shouldRetry(err, attempt, e.cfg.MaxTransientErrorRetries) {
			return "", err
		}
	}
}

func (e *runEngineImpl) tryForkRun(ctx context.Context, shardID int32, req *pb.ForkRunRequest) (string, errors.CategorizedError) {
	if req.ToEventId <= 0 {
		return "", errors.NewInvalidInputError("to_event_id must be positive", nil)
	}

	events, err := e.historyStore.GetHistoryEvents(ctx, req.Namespace, req.RunId, req.ToEventId-1, 1)
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		return "", errors.NewInvalidInputError(
			fmt.Sprintf("history event %d not found for run", req.ToEventId), nil)
	}
	targetEvent := events[0]

	runMutation, err := e.mutations.NewMutationForUpdate(ctx, shardID, req.Namespace, req.RunId)
	if err != nil {
		return "", err
	}
	previousWorkerID := runMutation.GetRun().WorkerID

	err = runMutation.ApplyForkRun(targetEvent)
	if err != nil {
		return "", err
	}
	runMutation.AddHistoryRunFork(req.ToEventId, req.Reason)
	runMutation.UpdateVisibilityIfStatusChanged()

	if commitErr := runMutation.Commit(ctx, nil); commitErr != nil {
		return "", commitErr
	}
	return previousWorkerID, nil
}

// ============================================================================
// Helpers
// ============================================================================

func shouldRetry(err errors.CategorizedError, attempt, maxRetries int) bool {
	if attempt >= maxRetries {
		return false
	}
	return err.IsRetriable()
}

func hasStepMethodOutcomeFailed(report *pb.StepMethodReport) bool {
	return report != nil && report.Outcome == pb.StepMethodOutcome_STEP_METHOD_OUTCOME_FAILED
}

// buildWorkerCallResponseWithCatchUp constructs the WorkerCallResponse appended to
// every worker→server API response. Carries unreceived external messages
// (catch-up, with blob-backed values resolved). stop_requested is set when the run is terminal.
// The catch-up payload (id > lastReceivedID) lets a worker that missed a
// best-effort PollForExternalEvents delivery converge on the next heartbeat /
// step completion.
func (e *runEngineImpl) buildWorkerCallResponseWithCatchUp(ctx context.Context, run *p.RunRow, lastReceivedID int64) (*pb.WorkerCallResponse, errors.CategorizedError) {
	unreceived, err := e.blobs.NewResolver(run).LoadAndResolveUnreceivedChannelMessagesSorted(ctx, lastReceivedID)
	if err != nil {
		return nil, err
	}
	return &pb.WorkerCallResponse{
		UnreceivedExternalChannelMessages: unreceived,
		StopRequested:                     run.Status.IsTerminal(),
	}, nil
}
