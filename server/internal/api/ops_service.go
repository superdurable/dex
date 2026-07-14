package api

import (
	"context"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	commonerrors "github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/internal/engine/blobs"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shardmanager"
)

// OpsServiceHandler implements pb.OpsServiceServer. It is a thin layer that
// adapts the persistence-level VisibilityStore + HistoryStore APIs to the
// gRPC surface and maps CategorizedErrors to gRPC status codes via the
// existing api.errors.ToProtoError helper.
//
// Blob hydration on read: history payloads are stored with the
// pb.Value_EncodedObjectBlobIdInternalOnly server-internal variant in place
// of EncodedObject (see engine/history_blob_walker.go). Before returning a
// page of pb.HistoryEvents to the wire, GetHistoryEvents collects every
// blob_id referenced across the page, fetches the blobs in a single
// BatchGetBlobs round trip, and rewrites every BlobIdInternalOnly back to
// EncodedObject in place. shard_id is computed from (namespace, run_id) via
// ShardMapper — ops-service deployments share the runs cluster's shard
// hashing config (see GetNumShards) so no per-doc shard_id is stored.
type OpsServiceHandler struct {
	pb.UnimplementedOpsServiceServer
	visibility  p.VisibilityStore
	history     p.HistoryStore
	blobStore   p.BlobStore
	shardMapper shardmanager.ShardMapper
	logger      log.Logger
}

func NewOpsServiceHandler(
	visibility p.VisibilityStore,
	history p.HistoryStore,
	blobStore p.BlobStore,
	shardMapper shardmanager.ShardMapper,
	logger log.Logger,
) *OpsServiceHandler {
	return &OpsServiceHandler{
		visibility:  visibility,
		history:     history,
		blobStore:   blobStore,
		shardMapper: shardMapper,
		logger:      logger,
	}
}

func (h *OpsServiceHandler) ListRuns(ctx context.Context, req *pb.ListRunsRequest) (*pb.ListRunsResponse, error) {
	// Proto3 field-presence: req.Status is nil when the client omits the
	// field (= "any status"). When set, translate to the persistence enum.
	var statusFilter *p.RunStatus
	if req.Status != nil {
		s := p.RunStatus(*req.Status)
		statusFilter = &s
	}
	q := p.ListRunsQuery{
		Namespace: req.Namespace,
		FlowType:  req.FlowType,
		Status:    statusFilter,
		OrderBy:   listRunsOrderBy(req.OrderBy),
		Limit:     int(req.Limit),
		PageToken: req.PageToken,
	}
	page, err := h.visibility.ListRuns(ctx, q)
	if err != nil {
		return nil, commonerrors.ToProtoError(err)
	}
	resp := &pb.ListRunsResponse{
		NextPageToken: page.NextPageToken,
		Runs:          make([]*pb.RunSummary, 0, len(page.Entries)),
	}
	for _, e := range page.Entries {
		resp.Runs = append(resp.Runs, &pb.RunSummary{
			RunId:        e.RunID,
			Namespace:    e.Namespace,
			FlowType:     e.FlowType,
			TaskListName: e.TaskListName,
			Status:       int32(e.Status),
			StartTimeMs:  e.StartTime.UnixMilli(),
			UpdatedAtMs:  e.UpdatedAt.UnixMilli(),
		})
	}
	return resp, nil
}

func (h *OpsServiceHandler) GetHistoryEvents(ctx context.Context, req *pb.GetHistoryEventsRequest) (*pb.GetHistoryEventsResponse, error) {
	events, err := h.history.GetHistoryEvents(ctx, req.Namespace, req.RunId, req.AfterId, int(req.Limit))
	if err != nil {
		return nil, commonerrors.ToProtoError(err)
	}
	resp := &pb.GetHistoryEventsResponse{
		Events: make([]*pb.HistoryEvent, 0, len(events)),
	}
	for _, e := range events {
		resp.Events = append(resp.Events, historyEventToPb(e))
	}

	if err := h.hydrateBlobs(ctx, req.Namespace, req.RunId, resp.Events); err != nil {
		return nil, commonerrors.ToProtoError(err)
	}
	return resp, nil
}

// hydrateBlobs rewrites every pb.Value_EncodedObjectBlobIdInternalOnly in
// the event page back to pb.Value_EncodedObject by fetching the referenced
// blobs in a single BatchGetBlobs call. Page-level batching keeps the read
// path at exactly one BlobStore round trip regardless of how many history
// events / Values the page contains.
func (h *OpsServiceHandler) hydrateBlobs(ctx context.Context, namespace, runID string, events []*pb.HistoryEvent) commonerrors.CategorizedError {
	if len(events) == 0 {
		return nil
	}
	var blobIDs []ids.BlobID
	blobs.CollectBlobIDsFromHistoryEvents(events, &blobIDs)
	if len(blobIDs) == 0 {
		return nil
	}
	shardID := h.shardMapper.GetShardID(namespace, runID)
	entries, err := h.blobStore.BatchGetBlobs(ctx, shardID, namespace, runID, blobIDs)
	if err != nil {
		return err
	}
	blobMap := make(map[ids.BlobID]p.BlobEntry, len(entries))
	for _, b := range entries {
		blobMap[b.BlobID] = b
	}
	for _, ev := range events {
		// A missing blob (TTL'd, evicted, or store data lost) is unrecoverable
		// data loss; fail the read loudly instead of masking it as null.
		if hydrateErr := blobs.HydrateBlobRefsToEncodedObjects(ev, blobMap); hydrateErr != nil {
			h.logger.Error("OpsService.GetHistoryEvents: history blob ref missing from BlobStore",
				tag.Namespace(namespace), tag.RunID(runID))
			return hydrateErr
		}
	}
	return nil
}

func listRunsOrderBy(o pb.ListRunsOrderBy) p.ListRunsOrderBy {
	switch o {
	case pb.ListRunsOrderBy_LIST_RUNS_ORDER_BY_UPDATED_AT_DESC:
		return p.ListByUpdatedAtDesc
	default:
		return p.ListByStartTimeDesc
	}
}

// historyEventToPb projects the persistence sum-type into the pb oneof.
// Exactly one variant on the persistence side is non-nil (HistoryStore
// validates this on insert and again on read decode), so the wire response
// always has exactly one oneof case set. Any blob_id refs inside the
// payload are still in BlobIdInternalOnly form here — caller (GetHistoryEvents)
// runs hydrateBlobs before responding.
func historyEventToPb(e p.HistoryEvent) *pb.HistoryEvent {
	out := &pb.HistoryEvent{
		Id:           e.EventID,
		OccurredAtMs: e.OccurredAtMs,
		WorkerId:     e.WorkerID,
	}
	switch {
	case e.Payload.RunStart != nil:
		out.Payload = &pb.HistoryEvent_RunStart{RunStart: e.Payload.RunStart}
	case e.Payload.RunStop != nil:
		out.Payload = &pb.HistoryEvent_RunStop{RunStop: e.Payload.RunStop}
	case e.Payload.StepExecuteCompleted != nil:
		out.Payload = &pb.HistoryEvent_StepExecuteCompleted{StepExecuteCompleted: e.Payload.StepExecuteCompleted}
	case e.Payload.StepWaitForCompleted != nil:
		out.Payload = &pb.HistoryEvent_StepWaitForCompleted{StepWaitForCompleted: e.Payload.StepWaitForCompleted}
	case e.Payload.ChannelPublish != nil:
		out.Payload = &pb.HistoryEvent_ChannelPublish{ChannelPublish: e.Payload.ChannelPublish}
	case e.Payload.StepsUnblocked != nil:
		out.Payload = &pb.HistoryEvent_StepsUnblocked{StepsUnblocked: e.Payload.StepsUnblocked}
	case e.Payload.RunFork != nil:
		out.Payload = &pb.HistoryEvent_RunFork{RunFork: e.Payload.RunFork}
	}
	return out
}
