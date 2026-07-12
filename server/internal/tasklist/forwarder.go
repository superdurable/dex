package tasklist

import (
	"context"
	"sync/atomic"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/cluster"
	"github.com/superdurable/dex/server/internal/metrics"
	"github.com/superdurable/dex/server/internal/routing"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/proto"
)

// pollForwarder is the subset of forwarder that matcher.Poll needs: a
// single non-blocking poll fan-in to the root partition. Abstracted so
// matcher unit tests can inject a fake without a real matching server.
type pollForwarder interface {
	ForwardPoll(ctx context.Context, workerID string) (*Task, errors.CategorizedError)
}

// forwarder enables non-root partitions to delegate sync-match attempts
// to their root partition. Two operations:
//   - ForwardTask: when DispatchRun misses the local poller on a non-root
//     partition, relay the full 3-message DispatchRun stream to root so
//     root's pollers get a sync-match chance before local DB write.
//   - ForwardPoll: when PollForRun arrives at a non-root partition with
//     no buffered task, register the poller at root so it can pick up
//     tasks dispatched directly to root.
//
// Concurrency limits via token-channel pattern (mirrors Cadence forwarder):
//   - addReqTokenC: limits in-flight ForwardTask RPCs (default 1)
//   - pollReqTokenC: limits in-flight ForwardPoll RPCs (default 1)
//
// Plus a per-partition rate limiter (default 10 RPS) on combined traffic.
//
// Only non-root partitions have a forwarder; the root has fwdr == nil
// (matcher.TryLocalOffer / Poll handle the nil case by going local-only).
type forwarder struct {
	id     *Identifier
	cfg    config.MatchingServiceConfig
	logger log.Logger

	membership   *cluster.Membership
	remoteClient *routing.RemoteClient

	addTokenCh  chan struct{}
	pollTokenCh chan struct{}
	limiter     *rate.Limiter

	stopped atomic.Bool
}

func newForwarder(
	id *Identifier,
	cfg config.MatchingServiceConfig,
	logger log.Logger,
	membership *cluster.Membership,
	remoteClient *routing.RemoteClient,
) *forwarder {
	if id.IsRoot() {
		// Root partition has no parent to forward to.
		return nil
	}

	maxOutstandingTasks := cfg.ForwarderMaxOutstandingTasks
	if maxOutstandingTasks < 1 {
		maxOutstandingTasks = 1
	}
	maxOutstandingPolls := cfg.ForwarderMaxOutstandingPolls
	if maxOutstandingPolls < 1 {
		maxOutstandingPolls = 1
	}
	rps := cfg.ForwarderMaxRatePerSecond
	if rps < 1 {
		rps = 10
	}

	addTokenCh := make(chan struct{}, maxOutstandingTasks)
	for i := 0; i < maxOutstandingTasks; i++ {
		addTokenCh <- struct{}{}
	}
	pollTokenCh := make(chan struct{}, maxOutstandingPolls)
	for i := 0; i < maxOutstandingPolls; i++ {
		pollTokenCh <- struct{}{}
	}

	return &forwarder{
		id:           id,
		cfg:          cfg,
		logger:       logger.WithTags(tag.Namespace(id.Namespace()), tag.TaskListName(id.FullName())),
		membership:   membership,
		remoteClient: remoteClient,
		addTokenCh:   addTokenCh,
		pollTokenCh:  pollTokenCh,
		limiter:      rate.NewLimiter(rate.Limit(rps), rps),
	}
}

// clientForRoot returns the matching client to use for fan-in calls to
// the root partition. Looks up the root partition's owner via membership
// and dials via RemoteClient
func (f *forwarder) clientForRoot() (pb.MatchingServiceClient, errors.CategorizedError) {
	rootID, err := NewIdentifier(f.id.Namespace(), f.id.BaseName(), 0)
	if err != nil {
		return nil, errors.NewInternalError("build root identifier: %w", err)
	}
	owner := f.membership.GetNodeForKey(rootID.String())
	addr := f.membership.GetAddress(owner)
	if addr == "" {
		return nil, errors.NewInternalError("root owner %q has no known address: "+owner, nil)
	}
	return f.remoteClient.GetMatchingServiceClient(addr)
}

// ForwardTask relays a DispatchRun sync-match attempt to the root
// partition.
//
// Return contract:
//   - (true, nil): root sync-matched and the Response (+ msg3 pump) was
//     delivered to upstream. Caller must NOT touch the upstream stream.
//   - (true, err): root matched and we already sent Response to taskProcessor,
//   - (false, nil): root missed. Upstream UNTOUCHED. Caller falls back
//     to local write.
//   - (false, err): failed before touching upstream (token/limit/dial/
//     send/recv). Upstream UNTOUCHED. Caller falls back to local write.
//
// Non-blocking on token acquisition.
func (f *forwarder) ForwardTask(ctx context.Context, req *pb.DispatchRunRequest, upstream pb.MatchingService_DispatchRunServer) (bool, errors.CategorizedError) {
	if f.stopped.Load() {
		return false, errors.NewUnavailableError("tasklist forwarder stopped", nil)
	}
	f.logger.Debug("forwarder.ForwardTask", tag.RunID(req.RunId), tag.Shard(req.ShardId))
	metrics.CounterForwardTaskAttempt.Inc(metrics.TagPartitionRole(false))
	// Try to acquire a token (non-blocking).
	select {
	case <-f.addTokenCh:
		defer func() { f.addTokenCh <- struct{}{} }()
	default:
		return false, errors.NewResourceExhaustedError("forward token is full")
	}
	if !f.limiter.Allow() {
		return false, errors.NewResourceExhaustedError("forward limit exceeded")
	}

	cli, clientErr := f.clientForRoot()
	if clientErr != nil {
		return false, clientErr
	}
	// Detach the root stream from the caller's ctx. The upstream
	// (taskprocessor) cancels its ctx right after sending msg3;
	// if root's stream chained to it, root's msg3 Recv would be cancelled
	// before we can relay msg3.
	rootCtx, rootCancel := context.WithTimeout(context.WithoutCancel(ctx), f.cfg.OperationTimeout)
	defer rootCancel()
	rootStream, err := cli.DispatchRun(rootCtx)
	if err != nil {
		return false, errors.NewUnavailableError("forward task: open root stream", err)
	}

	// Clone the original request, retarget it at root, mark it forwarded
	// so root serves it sync-match-only (no DB write on miss).
	fwdReq := proto.Clone(req).(*pb.DispatchRunRequest)
	fwdReq.ForwardedFromPartition = f.id.FullName()
	fwdReq.TaskListName = f.id.Parent()
	if err := rootStream.Send(&pb.EngineToMatchingDispatchMessage{
		Message: &pb.EngineToMatchingDispatchMessage_Request{Request: fwdReq},
	}); err != nil {
		_ = rootStream.CloseSend()
		return false, errors.NewUnavailableError("forward task: send request to root", err)
	}

	rootMsg, err := rootStream.Recv()
	if err != nil {
		_ = rootStream.CloseSend()
		return false, errors.NewUnavailableError("forward task: recv response from root", err)
	}
	rootResp := rootMsg.GetResponse()
	if rootResp == nil {
		_ = rootStream.CloseSend()
		return false, errors.NewInternalError("forward task: root sent unexpected message type", nil)
	}

	if !rootResp.SyncMatched {
		// Root missed — leave upstream untouched; caller writes locally.
		_ = rootStream.CloseSend()
		return false, nil
	}

	// Root sync-matched. From here we own the upstream Response: relay it,
	// then pump msg3 back to root. Any later failure returns (true, err).
	if err := upstream.Send(&pb.MatchingToEngineDispatchMessage{
		Message: &pb.MatchingToEngineDispatchMessage_Response{Response: rootResp},
	}); err != nil {
		_ = rootStream.CloseSend()
		return false, errors.NewUnavailableError("forward task: send response to upstream", err)
	}
	upMsg3, err := upstream.Recv()
	if err != nil {
		_ = rootStream.CloseSend()
		return true, errors.NewUnavailableError("forward task: recv msg3 from upstream", err)
	}
	pollResp := upMsg3.GetPollForRunResponse()
	if pollResp == nil || pollResp.RunId == "" {
		_ = rootStream.CloseSend()
		return true, errors.NewInvalidInputError("forward task: expected PollForRunResponse on msg3", nil)
	}
	if err := rootStream.Send(&pb.EngineToMatchingDispatchMessage{
		Message: &pb.EngineToMatchingDispatchMessage_PollForRunResponse{PollForRunResponse: pollResp},
	}); err != nil {
		_ = rootStream.CloseSend()
		return true, errors.NewUnavailableError("forward task: send msg3 to root", err)
	}
	_ = rootStream.CloseSend()
	metrics.CounterForwardTaskMatched.Inc(metrics.TagPartitionRole(false))
	f.logger.Debug("forwarder.ForwardTask matched at root", tag.RunID(req.RunId))
	return true, nil
}

// ForwardPoll registers a poll request at the root partition. If the
// root has a buffered task, it returns it. On failure err describes the
// actual cause: ErrForwarderStopped (shutting down), ErrForwarderRateLimited
// (rate limited), ctx.Err() (timeout), or a wrapped routing/RPC error from
// resolving the root client / calling PollForRun. (nil, nil) means root had
// no task ready.
//
// The returned Task may have been written by root or any non-root
// partition that itself forwarded to root — caller doesn't care.
//
// workerID is the REAL polling worker's id (not empty): root binds the
// dispatch to it via ProcessAsyncMatch / sync-match so the relayed
// PollForRunResponse targets the correct worker.
func (f *forwarder) ForwardPoll(ctx context.Context, workerID string) (*Task, errors.CategorizedError) {
	if f.stopped.Load() {
		return nil, errors.NewUnavailableError("tasklist forwarder stopped", nil)
	}
	metrics.CounterForwardPollAttempt.Inc(metrics.TagPartitionRole(false))
	select {
	case <-f.pollTokenCh:
		defer func() { f.pollTokenCh <- struct{}{} }()
	case <-ctx.Done():
		return nil, errors.NewTimeoutError("forwarding poll timeout", ctx.Err())
	}
	if !f.limiter.Allow() {
		return nil, errors.NewResourceExhaustedError("forward limit exceeded")
	}

	rootName := f.id.Parent()
	// Use a deadline slightly tighter than the parent ctx so root's
	// safety-buffer logic kicks in before our ctx fires.
	subCtx := ctx
	if deadline, ok := ctx.Deadline(); ok {
		buffer := f.cfg.LongPollSafetyBuffer
		if buffer <= 0 {
			buffer = 5 * time.Second
		}
		newDeadline := deadline.Add(-buffer / 2)
		if time.Until(newDeadline) > 0 {
			var cancel context.CancelFunc
			subCtx, cancel = context.WithDeadline(ctx, newDeadline)
			defer cancel()
		}
	}

	cli, clientErr := f.clientForRoot()
	if clientErr != nil {
		f.logger.Debug("forwarder.ForwardPoll: clientForRoot failed", tag.Error(clientErr))
		metrics.CounterForwardPollError.Inc(metrics.TagPartitionRole(false))
		return nil, errors.NewInternalError("forward poll to root: resolve client", clientErr)
	}
	resp, err := cli.PollForRun(subCtx, &pb.PollForRunRequest{
		Namespace:    f.id.Namespace(),
		TaskListName: rootName,
		WorkerId:     workerID,
		// Tell root to behave non-blockingly (TryLocalPoll → return empty if
		// nothing ready right now).
		ForwardedFromPartition: f.id.FullName(),
	})
	if err != nil {
		f.logger.Debug("forwarder.ForwardPoll: PollForRun failed", tag.Error(err))
		metrics.CounterForwardPollError.Inc(metrics.TagPartitionRole(false))
		return nil, errors.NewInternalError("forward poll to root: poll", err)
	}
	if resp == nil || resp.RunId == "" {
		metrics.CounterForwardPollEmpty.Inc(metrics.TagPartitionRole(false))
		return nil, nil
	}
	metrics.CounterForwardPollHit.Inc(metrics.TagPartitionRole(false))
	// Construct a synthetic Task wrapping the PollForRunResponse. The
	// matcher.Poll caller gets this Task; the PollForRun handler at this
	// partition relays the embedded PollForRunResponse to the worker as-is.
	task := &Task{
		runID:              resp.RunId,
		namespace:          f.id.Namespace(),
		taskID:             0, // forwarded — no local taskID
		pollRespDeliveryCh: make(chan *pb.PollForRunResponse, 1),
	}
	task.pollRespDeliveryCh <- resp
	f.logger.Debug("forwarder.ForwardPoll got task from root", tag.RunID(task.runID))
	return task, nil
}

func (f *forwarder) Stop() {
	if f == nil {
		// the constructor returns nil if id.IsRoot()
		return
	}
	f.stopped.Store(true)
}
