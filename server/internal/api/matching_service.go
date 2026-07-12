package api

import (
	"context"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/cluster"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/routing"
	"github.com/superdurable/dex/server/internal/tasklist"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// MatchingServiceHandler implements pb.MatchingServiceServer.
// Architecture (Cadence-style tasklist): this handler is a thin
// ownership-routing adapter. It resolves the partition (via Registry),
// checks ownership, and either routes to the owner node or delegates to
// the local partition's tasklist.Manager.
//
// Verb conventions (used both here and in the tasklist subpackage):
//
//   - forward / Forward*: PARTITION-TREE FAN-IN. A non-root partition
//     delegates to the root partition.
//   - route / route*: NODE-OWNERSHIP ROUTING.
type MatchingServiceHandler struct {
	pb.UnimplementedMatchingServiceServer

	cfg            config.MatchingServiceConfig
	logger         log.Logger
	registry       *tasklist.Registry
	stickyRegistry *tasklist.StickyRegistry
	membership     *cluster.Membership
	remoteClient   *routing.RemoteClient
}

type HandlerDeps struct {
	Config       config.MatchingServiceConfig
	Tasklist     config.TasklistConfig
	Logger       log.Logger
	Store        p.TasklistStore
	Membership   *cluster.Membership
	RemoteClient *routing.RemoteClient
	// RunsClient is the local runs-service loopback, required for the
	// async-pickup path (ProcessAsyncMatch).
	LocalRunsClient pb.RunsServiceClient
}

// NewMatchingServiceHandler constructs a new handler. Call Start to
// launch background goroutines (registry scan, sticky cleanup).
func NewMatchingServiceHandler(deps HandlerDeps) *MatchingServiceHandler {
	logger := deps.Logger.WithTags(tag.Component("matching"))

	registry := tasklist.NewRegistry(tasklist.RegistryDeps{
		Config:       deps.Config,
		Tasklist:     deps.Tasklist,
		Logger:       logger,
		Store:        deps.Store,
		RunsClient:   deps.LocalRunsClient,
		Membership:   deps.Membership,
		RemoteClient: deps.RemoteClient,
	})
	sticky := tasklist.NewStickyRegistry(deps.Config, logger)

	return &MatchingServiceHandler{
		cfg:            deps.Config,
		logger:         logger,
		registry:       registry,
		stickyRegistry: sticky,
		membership:     deps.Membership,
		remoteClient:   deps.RemoteClient,
	}
}

// Start launches the registry's ownership scan and the sticky registry's
// cleanup loop. Idempotent in practice — subsequent calls have no effect.
func (h *MatchingServiceHandler) Start() {
	h.registry.Start()
	h.stickyRegistry.Start()
	h.logger.Info("MatchingService started")
}

// Stop gracefully shuts down all background goroutines and tasklist
// managers. Idempotent.
func (h *MatchingServiceHandler) Stop() {
	h.stickyRegistry.Stop()
	h.registry.Stop()
	h.logger.Info("MatchingService stopped")
}

// ============================================================================
// PollForRun
// ============================================================================
func (h *MatchingServiceHandler) PollForRun(ctx context.Context, req *pb.PollForRunRequest) (*pb.PollForRunResponse, error) {
	h.logger.Debug("Matching.PollForRun", tag.Namespace(req.Namespace), tag.TaskListName(req.TaskListName), tag.WorkerID(req.WorkerId))
	if req.Namespace == "" || req.TaskListName == "" || req.WorkerId == "" {
		return nil, status.Error(codes.InvalidArgument, "namespace, task_list_name, worker_id required")
	}

	id, idErr := h.registry.ResolveReadPartition(req.Namespace, req.TaskListName)
	if idErr != nil {
		return nil, status.Error(codes.InvalidArgument, idErr.Error())
	}

	pollCtx, cancel, cerr := h.applyPollBudget(ctx)
	if cerr != nil {
		// Budget inside the safety window — reply empty, not an RPC error.
		return &pb.PollForRunResponse{}, nil
	}
	defer cancel()

	if !h.isOwner(id) {
		return h.routePollToOwner(pollCtx, id, req)
	}

	mgr, err := h.registry.GetOrCreateManager(pollCtx, id)
	if err != nil {
		return nil, errors.ToProtoError(err)
	}

	// req.ForwardedFromPartition != "" means it's forwarded from non-root.
	// So here must be its root. So We only do non-blocking polling to avoid waiting.
	// So that remote caller can fallback to a blocking poll.
	nonBlockingPoll := req.ForwardedFromPartition != ""
	resp, catErr := mgr.PollForRun(pollCtx, req.WorkerId, nonBlockingPoll)
	if catErr != nil {
		return nil, errors.ToProtoError(catErr)
	}
	return resp, nil
}

func (h *MatchingServiceHandler) routePollToOwner(ctx context.Context, id *tasklist.Identifier, req *pb.PollForRunRequest) (*pb.PollForRunResponse, error) {
	owner := h.membership.GetNodeForKey(id.String())
	addr := h.membership.GetAddress(owner)
	if addr == "" {
		return nil, status.Errorf(codes.Unavailable, "tasklist owner %q has no known address", owner)
	}
	cli, err := h.remoteClient.GetMatchingServiceClient(addr)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "dial owner: %v", err)
	}
	// !!!! we mutate the request in place !!!!
	req.TaskListName = id.FullName()
	return cli.PollForRun(ctx, req)
}

// ============================================================================
// DispatchRun
// ============================================================================
func (h *MatchingServiceHandler) DispatchRun(stream pb.MatchingService_DispatchRunServer) error {
	ctx := stream.Context()

	// Step 1: receive the initial Request.
	msg, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.Internal, "DispatchRun: recv request: %v", err)
	}
	req := msg.GetRequest()
	if req == nil || req.Namespace == "" || req.TaskListName == "" || req.RunId == "" {
		return status.Error(codes.InvalidArgument, "namespace, task_list_name, run_id required")
	}
	h.logger.Debug("Matching.DispatchRun", tag.RunID(req.RunId), tag.Namespace(req.Namespace), tag.TaskListName(req.TaskListName), tag.Shard(req.ShardId))

	id, idErr := h.registry.ResolveWritePartition(req.Namespace, req.TaskListName)
	if idErr != nil {
		return status.Error(codes.Internal, idErr.Error())
	}

	if !h.isOwner(id) {
		return h.routeDispatchToOwner(stream, req, id)
	}

	mgr, err := h.registry.GetOrCreateManager(ctx, id)
	if err != nil {
		return errors.ToProtoError(err)
	}
	return mgr.DispatchRun(ctx, req, stream)
}

func (h *MatchingServiceHandler) routeDispatchToOwner(
	upstream pb.MatchingService_DispatchRunServer,
	originalReq *pb.DispatchRunRequest,
	id *tasklist.Identifier,
) error {
	ctx := upstream.Context()
	owner := h.membership.GetNodeForKey(id.String())
	addr := h.membership.GetAddress(owner)
	if addr == "" {
		return status.Errorf(codes.Internal, "owner %q has no known address", owner)
	}
	cli, err := h.remoteClient.GetMatchingServiceClient(addr)
	if err != nil {
		return status.Errorf(codes.Internal, "dial owner: %v", err)
	}

	downStream, openErr := cli.DispatchRun(ctx)
	if openErr != nil {
		return status.Errorf(codes.Internal, "open owner stream: %v", openErr)
	}

	// !!! Mutate the request !!!
	originalReq.TaskListName = id.FullName()
	if sendErr := downStream.Send(&pb.EngineToMatchingDispatchMessage{
		Message: &pb.EngineToMatchingDispatchMessage_Request{Request: originalReq},
	}); sendErr != nil {
		_ = downStream.CloseSend()
		return status.Errorf(codes.Internal, "route send Request: %v", sendErr)
	}

	respMsg, recvErr := downStream.Recv()
	if recvErr != nil {
		_ = downStream.CloseSend()
		return status.Errorf(codes.Internal, "route recv Response: %v", recvErr)
	}
	resp := respMsg.GetResponse()
	if resp == nil {
		_ = downStream.CloseSend()
		return status.Error(codes.Internal, "owner sent unexpected message type")
	}
	if sendErr := upstream.Send(respMsg); sendErr != nil {
		_ = downStream.CloseSend()
		return status.Errorf(codes.Internal, "route send Response upstream: %v", sendErr)
	}

	// For async match, the task is written into DB its done
	if !resp.SyncMatched {
		_ = downStream.CloseSend()
		return nil
	}

	// For sync match, keep routing the remaining messages
	upMsg3, upRecvErr := upstream.Recv()
	if upRecvErr != nil {
		_ = downStream.CloseSend()
		return status.Errorf(codes.Internal, "route recv PollForRunResponse: %v", upRecvErr)
	}
	if sendErr := downStream.Send(upMsg3); sendErr != nil {
		_ = downStream.CloseSend()
		return status.Errorf(codes.Internal, "route send PollForRunResponse: %v", sendErr)
	}
	_ = downStream.CloseSend()
	return nil
}

// ============================================================================
// DeliverExternalEvents (best-effort, sticky, non-blocking)
// ============================================================================
func (h *MatchingServiceHandler) DeliverExternalEvents(ctx context.Context, req *pb.DeliverExternalEventsRequest) (*pb.DeliverExternalEventsResponse, error) {
	if req.WorkerId == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id required")
	}
	h.logger.Debug("Matching.DeliverExternalEvents", tag.WorkerID(req.WorkerId), tag.Count(len(req.Events)))
	var delivered int32
	for _, ev := range req.Events {
		if h.stickyRegistry.Deliver(req.WorkerId, ev) {
			delivered++
		}
	}
	return &pb.DeliverExternalEventsResponse{DeliveredCount: delivered}, nil
}

// ============================================================================
// PollForExternalEvents (long-poll, sticky)
// ============================================================================
func (h *MatchingServiceHandler) PollForExternalEvents(ctx context.Context, req *pb.PollForExternalEventsRequest) (*pb.PollForExternalEventsResponse, error) {
	if req.WorkerId == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id required")
	}
	h.logger.Debug("Matching.PollForExternalEvents", tag.Namespace(req.Namespace), tag.WorkerID(req.WorkerId))

	pollCtx, cancel, cerr := h.applyPollBudget(ctx)
	if cerr != nil {
		return &pb.PollForExternalEventsResponse{}, nil
	}
	defer cancel()

	event, err := h.stickyRegistry.Poll(pollCtx, req.WorkerId)
	if err != nil {
		return nil, err
	}
	if event == nil {
		return &pb.PollForExternalEventsResponse{}, nil
	}
	return &pb.PollForExternalEventsResponse{Events: []*pb.ExternalEvent{event}}, nil
}

// ============================================================================
// Helpers (shared)
// ============================================================================

// applyPollBudget shortens the inbound ctx by LongPollSafetyBuffer so
// the handler returns empty before the worker's deadline fires (avoids
// "took a task but couldn't deliver" races). Caps the deadline at
// LongPollDefaultTimeout.
//
// Only applies to blocking long-polls. The forwarded-poll path uses
// matcher.TryLocalPoll under the hood and does not consult ctx for waiting,
// so callers on that path skip this helper.
func (h *MatchingServiceHandler) applyPollBudget(ctx context.Context) (context.Context, context.CancelFunc, errors.CategorizedError) {
	defaultTimeout := h.cfg.LongPollDefaultTimeout
	if defaultTimeout <= 0 {
		defaultTimeout = 30 * time.Second
	}
	safetyBuffer := h.cfg.LongPollSafetyBuffer
	if safetyBuffer <= 0 {
		safetyBuffer = 5 * time.Second
	}

	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		ctx2, cancel := context.WithTimeout(ctx, defaultTimeout-safetyBuffer)
		return ctx2, cancel, nil
	}

	budget := time.Until(deadline) - safetyBuffer
	if budget <= 0 {
		return nil, nil, errors.NewCancelError("no enough timeout budget for polling request", nil)
	}
	if budget > defaultTimeout-safetyBuffer {
		budget = defaultTimeout - safetyBuffer
	}
	ctx2, cancel := context.WithTimeout(ctx, budget)
	return ctx2, cancel, nil
}

// isOwner reports whether this matching node owns the given partition
// per the membership hash-ring.
func (h *MatchingServiceHandler) isOwner(id *tasklist.Identifier) bool {
	owner := h.membership.GetNodeForKey(id.String())
	return owner == h.membership.MemberID()
}
