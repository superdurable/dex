package dex

import (
	"context"
	"fmt"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// applyCallResponse processes the WorkerCallResponse server-side stamp
// that piggybacks on every worker→server unary response: bumps
// last-seen-ext-msg-id from any catch-up payload, dispatches the catch-up
// messages onto the run's extChMsgInbox channel for runMain to merge, and sets
// stopRequested if the server is asking the worker to drain.
//
// Returns whether the stop is requested
func (w *Worker) applyCallResponse(runID string, resp *pb.WorkerCallResponse, inbox chan<- *pb.ExternalEvent) bool {
	for _, msg := range resp.UnreceivedExternalChannelMessages {
		// Synthesize an ExternalEvent so the same merge path in
		// runMain handles both push and catch-up. Wrap each missed
		// message into a single-message ChannelMessagesReceived
		// event keyed by run_id.
		event := &pb.ExternalEvent{
			RunId: runID,
			Event: &pb.ExternalEvent_ChannelMessagesReceived{
				ChannelMessagesReceived: &pb.ChannelMessagesReceived{
					ChannelName: msg.ChannelName,
					Messages:    []*pb.ChannelMessage{{Id: msg.Id, Value: msg.Value}},
				},
			},
		}
		select {
		case inbox <- event:
		default:
			// Inbox full; drop and rely on next catch-up. This is
			// correctness-safe but indicates RunInboxBufferSize is
			// undersized for this run's external-event volume —
			// raise it via WorkerOptions.RunInboxBufferSize.
			w.log.Error("Catch-up event dropped: extChMsgInbox full",
				"runID", runID, "channelName", msg.ChannelName, "id", msg.Id)
		}
	}
	if resp.StopRequested {
		return true
	}
	return false
}

// isRunOwnershipLost check whether err is a terminal status from the
// server indicating the run is no longer ours to act on. Caller must
// drop the run silently — no further worker→server RPCs and no
// retry. Covers:
//
//   - NotFound: run row gone.
//   - AlreadyExists: WorkerID mismatch (another worker has it).
//   - InvalidArgument: server-side state check failed (e.g. "expected
//     Running", worker_request_counter regression / gap).
//   - PermissionDenied / Unauthenticated: auth fatal — every RPC for
//     this worker will fail; safest to drop and let the worker either
//     die or continue with other runs that may not need auth (rare).
//   - Unimplemented: protocol version mismatch.
//
// All other gRPC codes (Unavailable, DeadlineExceeded, Internal,
// ResourceExhausted, Aborted, Canceled-from-server) are transient and
// caller should retry.
func isRunOwnershipLost(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.NotFound,
		codes.AlreadyExists,
		codes.InvalidArgument,
		codes.PermissionDenied,
		codes.Unauthenticated,
		codes.Unimplemented:
		return true
	}
	return false
}

// callRunRPC invokes call() with exponential-backoff retry on
// transient errors. It is the single entry point for every
// worker→server unary RPC carrying a per-run WorkerCallContext
// (heartbeat, step completion, unblock, release).
//
// Returns:
//   - ownershipLost=true when the server signals the run is no
//     longer ours (NotFound / AlreadyExists / InvalidArgument /
//     PermissionDenied / Unauthenticated / Unimplemented). Caller
//     should drop the run. err carries the underlying server error
//     for logging only.
//   - err=ctx.Err() when ctx is cancelled mid-retry (worker
//     shutting down). Caller should propagate.
//   - err=nil on eventual success.
//   - err set without ownershipLost when maxRetryDuration > 0 and
//     the retry budget is exhausted.
//
// maxRetryDuration:
//   - 0 means "retry until ctx done" — used for live-run RPCs
//     (heartbeat, step completion, checkpoint). The server's own
//     heartbeat-timeout will eventually transition the run to
//     WaitingForWorker if the network stays broken; that surfaces as
//     ownershipLost on the next RPC and the worker drops cleanly.
//   - >0 caps total retry time — used for ProcessReleaseRun on shutdown
//     (best-effort, must not block worker exit indefinitely).
// ctx gates the retry loop's cancellation. Pass w.rootCtx for live-run RPCs;
// pass a detached context for shutdown-time work (releaseRunBestEffort) that
// must still run after w.rootCtx is done.
func (w *Worker) callRunRPC(
	ctx context.Context,
	opName string,
	runID string,
	maxRetryDuration time.Duration,
	call func() error,
) (ownershipLost bool, err error) {
	var deadline time.Time
	if maxRetryDuration > 0 {
		deadline = time.Now().Add(maxRetryDuration)
	}
	backoff := time.Duration(0)
	maxBackoff := w.opts.pollErrorMaxBackoff()

	for attempt := 1; ; attempt++ {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		default:
		}
		if backoff > 0 {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(backoff):
			}
		}

		callErr := call()
		if callErr == nil {
			return false, nil
		}
		if isRunOwnershipLost(callErr) {
			w.log.Warn("RPC ownership lost; dropping run",
				"op", opName, "runID", runID, "error", callErr)
			return true, callErr
		}
		// Retry context done? If yes, propagate.
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		// Bounded retry budget exhausted (ProcessReleaseRun path)?
		if !deadline.IsZero() && time.Now().After(deadline) {
			return false, fmt.Errorf("%s: retry budget exhausted: %w", opName, callErr)
		}
		backoff = nextBackoff(backoff, maxBackoff)
		w.log.Warn("RPC transient error; retrying",
			"op", opName, "runID", runID,
			"attempt", attempt, "backoff", backoff, "error", callErr)
	}
}

func (w *Worker) sendHeartbeat(runID string, inbox chan<- *pb.ExternalEvent, callContext *pb.WorkerCallContext) (ownershipLost bool, err error) {
	var resp *pb.ProcessRecordHeartbeatResponse
	ownershipLost, err = w.callRunRPC(w.rootCtx, "ProcessRecordHeartbeat", runID, 0, func() error {
		var inner error
		ctx, cancel := context.WithTimeout(w.rootCtx, w.opts.regularRPCTimeout())
		defer cancel()
		resp, inner = w.runsClient.ProcessRecordHeartbeat(ctx, &pb.ProcessRecordHeartbeatRequest{
			Namespace: w.namespace,
			RunId:     runID,
			Context:   callContext,
		})
		return inner
	})
	if ownershipLost || err != nil {
		return ownershipLost, err
	}
	wcr := resp.GetWorkerCallResponse()
	w.applyCallResponse(runID, wcr, inbox)
	return false, nil
}

func (w *Worker) releaseRunAllStepsWaiting(runID string, callContext *pb.WorkerCallContext, inbox chan<- *pb.ExternalEvent) (exit bool, err error) {
	var resp *pb.ProcessReleaseRunResponse
	ownershipLost, rpcErr := w.callRunRPC(w.rootCtx, "ProcessReleaseRun", runID, 0, func() error {
		var inner error
		ctx, cancel := context.WithTimeout(w.rootCtx, w.opts.regularRPCTimeout())
		defer cancel()
		resp, inner = w.runsClient.ProcessReleaseRun(ctx, &pb.ProcessReleaseRunRequest{
			Namespace:     w.namespace,
			RunId:         runID,
			WorkerId:      w.workerID,
			ReleaseReason: pb.ReleaseRunReason_RELEASE_RUN_REASON_ALL_STEPS_WAITING,
			Context:       callContext,
		})
		return inner
	})
	if ownershipLost {
		w.log.Warn("ProcessReleaseRun ownership lost",
			"runID", runID)
		return true, nil
	}
	if rpcErr != nil {
		// Only reachable when rootCtx is cancelled (worker shutting down):
		// callRunRPC(maxRetryDuration=0) otherwise retries forever. Exit the
		// run loop gracefully instead of crashing.
		return true, rpcErr
	}
	wcr := resp.GetWorkerCallResponse()
	w.applyCallResponse(runID, wcr, inbox)
	if len(wcr.GetUnreceivedExternalChannelMessages()) > 0 {
		w.log.Info("ProcessReleaseRun not parked; catch-up applied",
			"runID", runID, "numExternalChannelMessages", len(wcr.GetUnreceivedExternalChannelMessages()))
		return false, nil
	}
	w.log.Info("run parked all_steps_waiting", "runID", runID)
	return true, nil
}

func (w *Worker) releaseRunBestEffort(pr *pb.PollForRunResponse) {

	// Detached context: this runs after rootCtx is already done (shutdown), so
	// callRunRPC must NOT gate on rootCtx here — the 60s budget bounds it.
	_, err := w.callRunRPC(context.Background(), "ProcessReleaseRun", pr.RunId, 60*time.Second, func() error {
		// NOTE: using a detached context because rootCtx is already done
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), w.opts.regularRPCTimeout())
		defer releaseCancel()
		_, inner := w.runsClient.ProcessReleaseRun(releaseCtx, &pb.ProcessReleaseRunRequest{
			Namespace:     pr.Namespace,
			RunId:         pr.RunId,
			WorkerId:      w.workerID,
			ReleaseReason: pb.ReleaseRunReason_RELEASE_RUN_REASON_YIELD_TO_ANOTHER_WORKER,
		})
		return inner
	})
	if err != nil {
		w.log.Warn("ProcessReleaseRun yield failed (best-effort)",
			"runID", pr.RunId, "error", err)
	}
}
