package testhelpers

import (
	"context"
	"fmt"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/internal/engine"
	"github.com/superdurable/dex/server/internal/shardmanager"
)

// DispatchRun opens a DispatchRun stream and runs the 3-message protocol.
func DispatchRun(ctx context.Context, matchClient pb.MatchingServiceClient, req *pb.DispatchRunRequest) (*pb.DispatchRunResponse, error) {
	stream, err := matchClient.DispatchRun(ctx)
	if err != nil {
		return nil, fmt.Errorf("DispatchRun stream open: %w", err)
	}
	if err := stream.Send(&pb.EngineToMatchingDispatchMessage{
		Message: &pb.EngineToMatchingDispatchMessage_Request{Request: req},
	}); err != nil {
		return nil, fmt.Errorf("DispatchRun send request: %w", err)
	}
	respMsg, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("DispatchRun recv response: %w", err)
	}
	resp := respMsg.GetResponse()
	if resp == nil {
		return nil, fmt.Errorf("DispatchRun: expected DispatchRunResponse, got %T", respMsg.GetMessage())
	}
	stream.CloseSend()
	return resp, nil
}

// DispatchRunWithEngine mirrors the real task-processor flow: after
// receiving sync_matched=true it marks the run Running via the engine
// (passing workerID from the Response), builds the PollForRunResponse, and
// sends it as msg3 so matching can deliver to the worker.
func DispatchRunWithEngine(
	ctx context.Context,
	matchClient pb.MatchingServiceClient,
	eng engine.RunEngine,
	mapper shardmanager.ShardMapper,
	req *pb.DispatchRunRequest,
) (*pb.DispatchRunResponse, error) {
	stream, err := matchClient.DispatchRun(ctx)
	if err != nil {
		return nil, fmt.Errorf("DispatchRun stream open: %w", err)
	}

	if err := stream.Send(&pb.EngineToMatchingDispatchMessage{
		Message: &pb.EngineToMatchingDispatchMessage_Request{Request: req},
	}); err != nil {
		return nil, fmt.Errorf("DispatchRun send request: %w", err)
	}

	respMsg, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("DispatchRun recv response: %w", err)
	}
	resp := respMsg.GetResponse()
	if resp == nil {
		return nil, fmt.Errorf("DispatchRun: expected DispatchRunResponse, got %T", respMsg.GetMessage())
	}

	shardID := mapper.GetShardID(req.Namespace, req.RunId)
	pollResp, casErr := eng.HandleRunDispatchResult(ctx, shardID, req.Namespace, req.RunId, resp.SyncMatched, resp.WorkerId)
	if casErr != nil {
		return nil, fmt.Errorf("HandleRunDispatchResult: %w", casErr)
	}

	if resp.SyncMatched && pollResp != nil {
		if err := stream.Send(&pb.EngineToMatchingDispatchMessage{
			Message: &pb.EngineToMatchingDispatchMessage_PollForRunResponse{PollForRunResponse: pollResp},
		}); err != nil {
			return nil, fmt.Errorf("DispatchRun send PollForRunResponse: %w", err)
		}
	}

	stream.CloseSend()
	return resp, nil
}
