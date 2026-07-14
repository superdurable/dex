package api

import (
	"context"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/engine"
	"github.com/superdurable/dex/server/internal/historynotify"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/routing"
	"github.com/superdurable/dex/server/internal/shardmanager"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RunsServiceHandler implements dexpb.RunsServiceServer.
// It is a thin layer that passes proto messages to the RunEngine and
// maps CategorizedErrors to gRPC status codes.
//
// Task wake-up notifications are emitted by the RunEngine itself after each
// successful task write — engine writes only happen on the shard owner
// (immediate-task SortKey allocation requires ownership), so a process-local
// notifier is sufficient and the API layer does not need to participate.
type RunsServiceHandler struct {
	pb.UnimplementedRunsServiceServer

	cfg    *config.RunServiceConfig
	logger log.Logger

	engine          engine.RunEngine
	shardMapper     shardmanager.ShardMapper
	shardManager    shardmanager.ShardManager
	historyNotifier historynotify.NotifierManager

	// localMatchingClient is the loopback client to this node's own matching
	// service, used by StopRun to issue a best-effort RunStopped
	// notification to the worker (run -> matching).
	localMatchingClient pb.MatchingServiceClient
	// remoteClient forwards StartRun / PublishToChannel / ProcessAsyncMatch
	// to the run service that owns the shard (run -> run cross-node routing).
	remoteClient *routing.RemoteClient
}

func NewRunsServiceHandler(
	eng engine.RunEngine,
	mapper shardmanager.ShardMapper,
	sm shardmanager.ShardManager,
	remoteClient *routing.RemoteClient,
	localMatchingClient pb.MatchingServiceClient,
	historyNotifier historynotify.NotifierManager,
	cfg *config.RunServiceConfig,
	logger log.Logger,
) *RunsServiceHandler {
	if localMatchingClient == nil {
		panic("NewRunsServiceHandler: localMatchingClient must not be nil")
	}
	if historyNotifier == nil {
		panic("NewRunsServiceHandler: historyNotifier must not be nil")
	}
	if cfg == nil {
		panic("NewRunsServiceHandler: cfg must not be nil")
	}
	return &RunsServiceHandler{
		engine:              eng,
		shardMapper:         mapper,
		shardManager:        sm,
		remoteClient:        remoteClient,
		localMatchingClient: localMatchingClient,
		historyNotifier:     historyNotifier,
		cfg:                 cfg,
		logger:              logger,
	}
}

func (h *RunsServiceHandler) StartRun(ctx context.Context, req *pb.StartRunRequest) (*pb.StartRunResponse, error) {
	// Forward to shard owner if not local (required for TaskSeq allocation)
	shardID := h.shardMapper.GetShardID(req.Namespace, req.RunId)
	if fwd, err := h.tryForward(ctx, shardID); fwd != nil || err != nil {
		if err != nil {
			return nil, err
		}
		fwdCtx, fwdCancel := context.WithTimeout(ctx, 10*time.Second)
		defer fwdCancel()
		return fwd.StartRun(fwdCtx, req)
	}

	if err := h.engine.StartRun(ctx, req); err != nil {
		return nil, errors.ToProtoError(err)
	}
	return &pb.StartRunResponse{}, nil
}

func (h *RunsServiceHandler) PublishToChannel(ctx context.Context, req *pb.PublishToChannelRequest) (*pb.PublishToChannelResponse, error) {
	// Forward to shard owner if not local (required for TaskSeq allocation)
	shardID := h.shardMapper.GetShardID(req.Namespace, req.RunId)
	if fwd, err := h.tryForward(ctx, shardID); fwd != nil || err != nil {
		if err != nil {
			return nil, err
		}
		fwdCtx, fwdCancel := context.WithTimeout(ctx, 10*time.Second)
		defer fwdCancel()
		return fwd.PublishToChannel(fwdCtx, req)
	}

	if err := h.engine.PublishExternalChannelMessages(ctx, shardID, req); err != nil {
		return nil, errors.ToProtoError(err)
	}
	return &pb.PublishToChannelResponse{}, nil
}

// StopRun CAS-transitions the run to Completed or Failed per stop_decision
// and best-effort notifies the active worker (if any) so it can cancel in-flight execution.
//
// Semantics:
//   - Idempotent: stopping an already-terminal run returns success without
//     modifying state.
//   - NotFound is returned if the run does not exist (authoritative — the
//     pre-check reads from primary).
//   - The worker notification is fire-and-forget on a background goroutine
//     bounded by RunServiceConfig.MatchingServiceAPITimeout. The handler
//     returns as soon as the status CAS commits, so a slow / unreachable
//     matching service never blocks the caller or the immediate task queue.
func (h *RunsServiceHandler) StopRun(ctx context.Context, req *pb.StopRunRequest) (*pb.StopRunResponse, error) {
	shardID := h.shardMapper.GetShardID(req.Namespace, req.RunId)
	if fwd, err := h.tryForward(ctx, shardID); fwd != nil || err != nil {
		if err != nil {
			return nil, err
		}
		fwdCtx, fwdCancel := context.WithTimeout(ctx, 10*time.Second)
		defer fwdCancel()
		return fwd.StopRun(fwdCtx, req)
	}

	wasActive, _, workerID, eErr := h.engine.StopRun(ctx, req.Namespace, req.RunId, req.GetStopDecision(), req.GetReason())
	if eErr != nil {
		return nil, errors.ToProtoError(eErr)
	}

	if wasActive && workerID != "" {
		h.bestEffortNotifyWorkerToStopRun(req.Namespace, workerID, req.RunId)
	}

	return &pb.StopRunResponse{}, nil
}

func (h *RunsServiceHandler) ForkRun(ctx context.Context, req *pb.ForkRunRequest) (*pb.ForkRunResponse, error) {
	shardID := h.shardMapper.GetShardID(req.Namespace, req.RunId)
	if fwd, err := h.tryForward(ctx, shardID); fwd != nil || err != nil {
		if err != nil {
			return nil, err
		}
		fwdCtx, fwdCancel := context.WithTimeout(ctx, 10*time.Second)
		defer fwdCancel()
		return fwd.ForkRun(fwdCtx, req)
	}

	previousWorkerID, eErr := h.engine.ForkRun(ctx, req)
	if eErr != nil {
		return nil, errors.ToProtoError(eErr)
	}

	if previousWorkerID != "" {
		h.bestEffortNotifyWorkerToStopRun(req.Namespace, previousWorkerID, req.RunId)
	}

	return &pb.ForkRunResponse{}, nil
}

// WaitForHistoryEvent long-polls until the request's condition is met, the run
// closes, or the caller's ctx deadline elapses.
func (h *RunsServiceHandler) WaitForHistoryEvent(ctx context.Context, req *pb.WaitForHistoryEventRequest) (*pb.WaitForHistoryEventResponse, error) {
	if req.GetCondition() == nil {
		return nil, errors.ToProtoError(errors.NewInvalidInputError("WaitForHistoryEvent: condition must be set", nil))
	}
	shardID := h.shardMapper.GetShardID(req.Namespace, req.RunId)
	if fwd, err := h.tryForward(ctx, shardID); fwd != nil || err != nil {
		if err != nil {
			return nil, err
		}
		// Propagate the caller's ctx unchanged: the owner blocks on it and any
		// forward failure surfaces directly to the caller.
		h.logger.Debug("WaitForHistoryEvent forwarding to shard owner",
			tag.RunID(req.RunId), tag.Namespace(req.Namespace))
		return fwd.WaitForHistoryEvent(ctx, req)
	}
	return h.waitForHistoryEventLocal(ctx, req)
}

func (h *RunsServiceHandler) waitForHistoryEventLocal(ctx context.Context, req *pb.WaitForHistoryEventRequest) (*pb.WaitForHistoryEventResponse, error) {
	sub, err := h.historyNotifier.Subscribe(ctx, req)
	if err != nil {
		return nil, errors.ToProtoError(err)
	}
	defer sub.Close()

	met := sub.WaitUntilConditionMet()
	// Fast path: the authoritative read at Subscribe already satisfied the
	// condition. Prefer it over an already-expired ctx.
	select {
	case res := <-met:
		return waitForHistoryResponse(res), nil
	default:
	}

	// Cap a single RPC's block at WaitForHistoryMaxTimeout
	pollCtx, cancel := context.WithTimeout(ctx, h.cfg.WaitForHistoryMaxTimeout)
	defer cancel()

	select {
	case res := <-met:
		return waitForHistoryResponse(res), nil
	case <-pollCtx.Done():
		h.logger.Debug("WaitForHistoryEvent deadline reached",
			tag.RunID(req.RunId), tag.Namespace(req.Namespace))
		return nil, status.FromContextError(pollCtx.Err()).Err()
	}
}

func waitForHistoryResponse(res historynotify.Result) *pb.WaitForHistoryEventResponse {
	return &pb.WaitForHistoryEventResponse{LatestEventId: res.EventID, RunStatus: int32(res.RunStatus)}
}

// ProcessStepExecuteCompleted is called directly by the worker after a step's
// Execute method returns. The worker gets namespace from PollResponse.
//
// Forwarded to the shard owner because step completion may write a durable
// wait-for TimerTaskRow; we want all task writes to land on the owner so the
// timer reader on that node can be notified directly and the deleter doesn't
// race with cross-node timer creation.
func (h *RunsServiceHandler) ProcessStepExecuteCompleted(ctx context.Context, req *pb.StepExecuteCompletedRequest) (*pb.StepExecuteCompletedResponse, error) {
	shardID := h.shardMapper.GetShardID(req.Namespace, req.RunId)
	if fwd, err := h.tryForward(ctx, shardID); fwd != nil || err != nil {
		if err != nil {
			return nil, err
		}
		fwdCtx, fwdCancel := context.WithTimeout(ctx, 10*time.Second)
		defer fwdCancel()
		return fwd.ProcessStepExecuteCompleted(fwdCtx, req)
	}
	outcome, err := h.engine.ProcessStepExecuteCompleted(ctx, shardID, req.Namespace, req)
	if err != nil {
		return nil, errors.ToProtoError(err)
	}
	return &pb.StepExecuteCompletedResponse{
		RunId:              req.RunId,
		WorkerCallResponse: outcome,
	}, nil
}

// ProcessStepsUnblocked durably checkpoints worker-driven sibling unblocks
// triggered out of band of any step completion (external channel delivery
// or local-timer fire while status=Running). See the proto doc on
// RunsService.ProcessStepsUnblocked and docs/wait-for-conditions-design.md
// for the correctness argument.
//
// Forwarded to the shard owner for the same reason as ProcessStepExecuteCompleted.
func (h *RunsServiceHandler) ProcessStepsUnblocked(ctx context.Context, req *pb.StepsUnblockedRequest) (*pb.StepsUnblockedResponse, error) {
	shardID := h.shardMapper.GetShardID(req.Namespace, req.RunId)
	if fwd, err := h.tryForward(ctx, shardID); fwd != nil || err != nil {
		if err != nil {
			return nil, err
		}
		fwdCtx, fwdCancel := context.WithTimeout(ctx, 10*time.Second)
		defer fwdCancel()
		return fwd.ProcessStepsUnblocked(fwdCtx, req)
	}
	outcome, err := h.engine.ProcessStepsUnblocked(ctx, shardID, req)
	if err != nil {
		return nil, errors.ToProtoError(err)
	}
	return &pb.StepsUnblockedResponse{
		WorkerCallResponse: outcome,
	}, nil
}

// ProcessStepWaitForCompleted is called directly by the worker after a step's
// WaitFor method returns. The worker gets namespace from PollResponse.
//
// Forwarded to the shard owner for the same reason as ProcessStepExecuteCompleted.
func (h *RunsServiceHandler) ProcessStepWaitForCompleted(ctx context.Context, req *pb.StepWaitForCompletedRequest) (*pb.StepWaitForCompletedResponse, error) {
	shardID := h.shardMapper.GetShardID(req.Namespace, req.RunId)
	if fwd, err := h.tryForward(ctx, shardID); fwd != nil || err != nil {
		if err != nil {
			return nil, err
		}
		fwdCtx, fwdCancel := context.WithTimeout(ctx, 10*time.Second)
		defer fwdCancel()
		return fwd.ProcessStepWaitForCompleted(fwdCtx, req)
	}
	outcome, err := h.engine.ProcessStepWaitForCompleted(ctx, shardID, req.Namespace, req)
	if err != nil {
		return nil, errors.ToProtoError(err)
	}
	return &pb.StepWaitForCompletedResponse{
		RunId:              req.RunId,
		WorkerCallResponse: outcome,
	}, nil
}

// GetRun returns the full run state with blob refs resolved.
// The caller can filter by status to only return if the run matches.
func (h *RunsServiceHandler) GetRun(ctx context.Context, req *pb.GetRunRequest) (*pb.GetRunResponse, error) {
	statusFilter := make([]p.RunStatus, len(req.StatusFilter))
	for i, s := range req.StatusFilter {
		statusFilter[i] = p.RunStatus(s)
	}
	resp, err := h.engine.GetRun(ctx, req.Namespace, req.RunId, statusFilter)
	if err != nil {
		return nil, errors.ToProtoError(err)
	}
	return resp, nil
}

// ProcessAsyncMatch transitions a single run to Running for async match
// pickup. Called by the matching service's PollForRun handler when an
// async-pickup task (pulled from the tasklist DB) needs to be claimed by
// a specific worker. Returns a PollForRunResponse for the worker on success.
func (h *RunsServiceHandler) ProcessAsyncMatch(ctx context.Context, req *pb.ProcessAsyncMatchRequest) (*pb.ProcessAsyncMatchResponse, error) {
	shardID := h.shardMapper.GetShardID(req.Namespace, req.RunId)
	if fwd, err := h.tryForward(ctx, shardID); fwd != nil || err != nil {
		if err != nil {
			return nil, err
		}
		fwdCtx, fwdCancel := context.WithTimeout(ctx, 10*time.Second)
		defer fwdCancel()
		return fwd.ProcessAsyncMatch(fwdCtx, req)
	}

	pollResp, casErr := h.engine.HandleRunDispatchResult(ctx, shardID, req.Namespace, req.RunId, true, req.WorkerId)
	if casErr != nil {
		if casErr.IsNotFoundError() || casErr.IsConflictError() {
			return &pb.ProcessAsyncMatchResponse{
				Outcome: pb.AsyncMatchOutcome_ASYNC_MATCH_OUTCOME_STALE_SUCCESS,
			}, nil
		}
		return nil, errors.ToProtoError(casErr)
	}
	if pollResp == nil {
		return &pb.ProcessAsyncMatchResponse{
			Outcome: pb.AsyncMatchOutcome_ASYNC_MATCH_OUTCOME_STALE_SUCCESS,
		}, nil
	}
	return &pb.ProcessAsyncMatchResponse{
		PollForRunResponse: pollResp,
		Outcome:            pb.AsyncMatchOutcome_ASYNC_MATCH_OUTCOME_SUCCESS,
	}, nil
}

// ProcessRecordHeartbeat handles a worker's periodic heartbeat. Validates
// WorkerID + WorkerRequestCounter, renews the heartbeat timer, and returns
// any unreceived external channel messages plus the StopRequested flag.
func (h *RunsServiceHandler) ProcessRecordHeartbeat(ctx context.Context, req *pb.ProcessRecordHeartbeatRequest) (*pb.ProcessRecordHeartbeatResponse, error) {
	shardID := h.shardMapper.GetShardID(req.Namespace, req.RunId)
	if fwd, err := h.tryForward(ctx, shardID); fwd != nil || err != nil {
		if err != nil {
			return nil, err
		}
		fwdCtx, fwdCancel := context.WithTimeout(ctx, 10*time.Second)
		defer fwdCancel()
		return fwd.ProcessRecordHeartbeat(fwdCtx, req)
	}

	resp, err := h.engine.ProcessRecordHeartbeat(ctx, shardID, req)
	if err != nil {
		return nil, errors.ToProtoError(err)
	}
	return &pb.ProcessRecordHeartbeatResponse{WorkerCallResponse: resp}, nil
}

// ProcessReleaseRun handles worker release: yield on shutdown or park all-steps-waiting.
func (h *RunsServiceHandler) ProcessReleaseRun(ctx context.Context, req *pb.ProcessReleaseRunRequest) (*pb.ProcessReleaseRunResponse, error) {
	shardID := h.shardMapper.GetShardID(req.Namespace, req.RunId)
	if fwd, err := h.tryForward(ctx, shardID); fwd != nil || err != nil {
		if err != nil {
			return nil, err
		}
		fwdCtx, fwdCancel := context.WithTimeout(ctx, 10*time.Second)
		defer fwdCancel()
		return fwd.ProcessReleaseRun(fwdCtx, req)
	}

	resp, err := h.engine.ProcessReleaseRun(ctx, shardID, req)
	if err != nil {
		return nil, errors.ToProtoError(err)
	}
	return resp, nil
}

// tryForward checks if the shard is local. If not, returns a
// RunsServiceClient for the shard owner so the caller can forward the
// request. Returns (nil, nil) when the shard is local OR forwarding is
// disabled (single-mode, or shardManager / forwarder not wired) — in
// which case the caller must handle the request itself.
func (h *RunsServiceHandler) tryForward(_ context.Context, shardID int32) (pb.RunsServiceClient, error) {
	if h.shardManager == nil || h.remoteClient == nil {
		return nil, nil
	}
	if h.shardManager.IsLocalShard(shardID) {
		return nil, nil
	}
	ownerAddr := h.shardManager.GetShardOwnerAddress(shardID)
	if ownerAddr == "" {
		return nil, nil
	}
	client, err := h.remoteClient.GetRunsServiceClient(ownerAddr)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "failed to connect to shard owner for forwarding")
	}
	return client, nil
}

func (h *RunsServiceHandler) bestEffortNotifyWorkerToStopRun(namespace, workerId, runId string) {
	mc := h.localMatchingClient
	timeout := h.cfg.MatchingServiceAPITimeout
	go func() {
		notifyCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if _, err := mc.DeliverExternalEvents(notifyCtx, &pb.DeliverExternalEventsRequest{
			Namespace: namespace,
			WorkerId:  workerId,
			Events: []*pb.ExternalEvent{{
				RunId: runId,
				Event: &pb.ExternalEvent_StopRequested{
					StopRequested: &pb.StopRequested{},
				},
			}},
		}); err != nil {
			h.logger.Info("notify worker failed (best-effort)",
				tag.RunID(runId), tag.Namespace(namespace),
				tag.Error(err))
		}
	}()
}
