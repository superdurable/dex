package engine

import (
	"context"
	"fmt"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/internal/engine/mutation"
	"github.com/superdurable/dex/server/internal/metrics"
	p "github.com/superdurable/dex/server/internal/persistence"
)

func (e *runEngineImpl) ForkRun(ctx context.Context, req *pb.ForkRunRequest) (string, errors.CategorizedError) {
	e.logger.Debug("RunEngine.ForkRun", tag.RunID(req.RunId), tag.Namespace(req.Namespace), tag.Value(req.ToEventId))
	shardID := e.shardMapper.GetShardID(req.Namespace, req.RunId)

	start := time.Now()
	outcome := metrics.ForkRunOutcomeSuccess
	defer func() {
		metrics.CounterForkRunRequests.Inc(metrics.TagForkRunOutcome(outcome))
		metrics.LatencyForkRun.Record(time.Since(start))
	}()

	for attempt := 0; ; attempt++ {
		previousWorkerID, err := e.tryForkRun(ctx, shardID, req)
		if err == nil {
			e.logger.Info("Run forked",
				tag.Namespace(req.Namespace), tag.RunID(req.RunId), tag.Value(req.ToEventId))
			return previousWorkerID, nil
		}
		if err.IsInvalidInputError() {
			outcome = metrics.ForkRunOutcomeInvalid
			return "", err
		}
		if err.IsConflictError() {
			if !shouldRetry(err, attempt, e.cfg.MaxTransientErrorRetries) {
				outcome = metrics.ForkRunOutcomeConflict
				return "", err
			}
			continue
		}
		outcome = metrics.ForkRunOutcomeInternal
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
	if len(events) == 0 || events[0].EventID != req.ToEventId {
		return "", errors.NewInvalidInputError(
			fmt.Sprintf("history event %d not found for run", req.ToEventId), nil)
	}
	targetEvent := events[0]

	runMutation, err := e.mutations.NewMutationForUpdate(ctx, shardID, req.Namespace, req.RunId)
	if err != nil {
		return "", err
	}
	previousWorkerID := runMutation.GetRun().WorkerID

	if validateErr := validateForkTargetEvent(targetEvent.Payload); validateErr != nil {
		return "", validateErr
	}

	stateMap, channels, counters, activeSteps, externalCounter := forkRestoreFields(targetEvent.Payload)
	runMutation.ApplyForkRestore(stateMap, channels, counters, activeSteps, externalCounter)
	runMutation.AddHistoryRunFork(req.ToEventId, req.Reason)
	runMutation.UpdateVisibilityIfStatusChanged()

	if commitErr := runMutation.Commit(ctx, nil); commitErr != nil {
		return "", commitErr
	}
	return previousWorkerID, nil
}

func validateForkTargetEvent(payload p.HistoryEventPayload) errors.CategorizedError {
	switch {
	case payload.RunStart != nil:
		return nil
	case payload.RunFork != nil:
		return errors.NewInvalidInputError("cannot fork to a run_fork marker event", nil)
	case payload.RunStop != nil:
		return errors.NewInvalidInputError("cannot fork to run_stop event", nil)
	case payload.ChannelPublish != nil:
		return errors.NewInvalidInputError("cannot fork to channel_publish event", nil)
	case payload.StepExecuteCompleted != nil:
		if payload.StepExecuteCompleted.StopDecision == pb.StopDecision_STOP_DECISION_COMPLETE ||
			payload.StepExecuteCompleted.StopDecision == pb.StopDecision_STOP_DECISION_FAIL {
			return errors.NewInvalidInputError("cannot fork to terminal step_execute_completed event", nil)
		}
		if payload.StepExecuteCompleted.Snapshot == nil {
			return errors.NewInvalidInputError("history event has no snapshot (written before fork support)", nil)
		}
		return nil
	case payload.StepWaitForCompleted != nil:
		if payload.StepWaitForCompleted.Snapshot == nil {
			return errors.NewInvalidInputError("history event has no snapshot (written before fork support)", nil)
		}
		return nil
	case payload.StepsUnblocked != nil:
		if payload.StepsUnblocked.Snapshot == nil {
			return errors.NewInvalidInputError("history event has no snapshot (written before fork support)", nil)
		}
		return nil
	default:
		return errors.NewInvalidInputError("unsupported history event type for fork", nil)
	}
}

func forkRestoreFields(payload p.HistoryEventPayload) (
	stateMap map[string]p.Value,
	channels map[string][]p.ChannelMessage,
	counters map[string]int32,
	activeSteps map[string]p.ActiveStepExecution,
	externalCounter int64,
) {
	if payload.RunStart != nil {
		return mutation.ForkRestoreFromRunStart(payload.RunStart)
	}
	var snapshot *pb.RunStateSnapshot
	switch {
	case payload.StepExecuteCompleted != nil:
		snapshot = payload.StepExecuteCompleted.Snapshot
	case payload.StepWaitForCompleted != nil:
		snapshot = payload.StepWaitForCompleted.Snapshot
	case payload.StepsUnblocked != nil:
		snapshot = payload.StepsUnblocked.Snapshot
	}
	return mutation.SnapshotToPersistence(snapshot)
}
