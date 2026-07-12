package dex

import (
	"context"
	"fmt"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"google.golang.org/grpc"
)

// RunResult holds the raw result of a GetRun call.
type RunResult struct {
	RunID        string
	Status       int32
	State        map[string]*pb.Value
	FlowType     string
	TaskListName string
}

// RawClient is an untyped gRPC client that wraps RunsServiceClient.
// It does not require a Registry and works with raw flow type strings.
type RawClient struct {
	runsClient pb.RunsServiceClient
	namespace  string
	codec      ObjectCodec
}

// NewRawClient creates a RawClient connected to the given gRPC connection.
// codec may be nil for DefaultObjectCodec.
func NewRawClient(runsConn grpc.ClientConnInterface, namespace string, codec ObjectCodec) *RawClient {
	if codec == nil {
		codec = DefaultObjectCodec()
	}
	return &RawClient{
		runsClient: pb.NewRunsServiceClient(runsConn),
		namespace:  namespace,
		codec:      codec,
	}
}

// StartRun starts a new flow run on the server.
func (c *RawClient) StartRun(ctx context.Context, runID, flowType, taskListName string, startingSteps []*pb.NextStep) error {
	req := &pb.StartRunRequest{
		Namespace:     c.namespace,
		RunId:         runID,
		FlowType:      flowType,
		TaskListName:  taskListName,
		StartingSteps: startingSteps,
	}
	_, err := callWithRetry(ctx, func(ctx context.Context) (*pb.StartRunResponse, error) {
		return c.runsClient.StartRun(ctx, req)
	})
	if err != nil {
		return fmt.Errorf("dex: StartRun: %w", err)
	}
	return nil
}

// StopRun asks the server to stop the run identified by runID.
//
// stopDecision must be StopRunComplete or StopRunFail: on success the run is
// durably transitioned to Completed or Failed (CAS on the primary). reason is
// optional user text stored on the RunStop history event (empty string if
// omitted). The server additionally pushes a best-effort StopRequested event
// to the worker that may be actively executing the run via the sticky
// PollForExternalEvents stream; the SDK Worker will cancel the per-run
// context and exit the run loop on receipt.
//
// Errors:
//   - codes.NotFound: no run with the given (namespace, runID) was found
//     (authoritative — the server's pre-check reads from primary).
//   - codes.InvalidArgument: stop_decision was not COMPLETE or FAIL, or reason
//     exceeded the server's max length.
//   - other gRPC codes: transient transport / server errors; the run state
//     is not modified by failed calls.
//
// Stopping an already-terminal run is idempotent and returns nil.
func (c *RawClient) StopRun(ctx context.Context, runID string, stopDecision pb.StopDecision, reason string) error {
	req := &pb.StopRunRequest{
		Namespace:    c.namespace,
		RunId:        runID,
		StopDecision: stopDecision,
		Reason:       reason,
	}
	_, err := callWithRetry(ctx, func(ctx context.Context) (*pb.StopRunResponse, error) {
		return c.runsClient.StopRun(ctx, req)
	})
	if err != nil {
		return fmt.Errorf("dex: StopRun: %w", err)
	}
	return nil
}

// PublishToChannel publishes one or more values to a named channel on a
// running run. Values are codec-encoded and sent as the wire
// PublishToChannelRequest.Values; the server appends them to
// RunRow.UnconsumedChannelMessages and either dispatches the run (if
// AllStepsWaiting) or pushes them via the sticky PollForExternalEvents
// channel to the active worker (if Running).
func (c *RawClient) PublishToChannel(ctx context.Context, runID, channelName string, values ...any) error {
	pbValues := make([]*pb.Value, 0, len(values))
	for i, v := range values {
		pv, err := c.codec.EncodeValue(v)
		if err != nil {
			return fmt.Errorf("dex: PublishToChannel: encode value[%d]: %w", i, err)
		}
		pbValues = append(pbValues, pv)
	}
	req := &pb.PublishToChannelRequest{
		Namespace:   c.namespace,
		RunId:       runID,
		ChannelName: channelName,
		Values:      pbValues,
	}

	_, err := callWithRetry(ctx, func(ctx context.Context) (*pb.PublishToChannelResponse, error) {
		return c.runsClient.PublishToChannel(ctx, req)
	})
	if err != nil {
		return fmt.Errorf("dex: PublishToChannel: %w", err)
	}
	return nil
}

// GetRun retrieves the current state of a run.
// statusFilter is optional: if non-empty, the server only returns the run
// when its status matches one of the given values (otherwise Found=false).
func (c *RawClient) GetRun(ctx context.Context, runID string, statusFilter ...int32) (*RunResult, error) {
	resp, err := callWithRetry(ctx, func(ctx context.Context) (*pb.GetRunResponse, error) {
		return c.runsClient.GetRun(ctx, &pb.GetRunRequest{
			Namespace:    c.namespace,
			RunId:        runID,
			StatusFilter: statusFilter,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("dex: GetRun: %w", err)
	}
	if !resp.Found {
		if len(statusFilter) > 0 {
			return nil, nil
		}
		return nil, newRunNotFoundError(runID)
	}

	return &RunResult{
		RunID:        resp.RunId,
		Status:       resp.Status,
		State:        resp.State,
		FlowType:     resp.FlowType,
		TaskListName: resp.TaskListName,
	}, nil
}

// WaitForHistoryEvent long-polls until the run's latest inserted history event
// id reaches untilEventID (or the run closes) and returns that latest event id.
// The wait is bounded by ctx (the server blocks on the same deadline); a
// codes.DeadlineExceeded means the event had not arrived by ctx.
func (c *RawClient) WaitForHistoryEvent(ctx context.Context, runID string, untilEventID int64) (int64, error) {
	resp, err := c.waitForHistoryEvent(ctx, &pb.WaitForHistoryEventRequest{
		Namespace: c.namespace,
		RunId:     runID,
		Condition: &pb.WaitForHistoryEventRequest_UntilEventId{UntilEventId: untilEventID},
	})
	if err != nil {
		return 0, err
	}
	return resp.LatestEventId, nil
}

// WaitForRunStop long-polls until the run reaches a terminal state and returns
// its terminal RunStatus. Bounded by ctx (the server blocks on the same
// deadline); a codes.DeadlineExceeded means the run had not closed by ctx.
// Backs Client.WaitForRunComplete.
func (c *RawClient) WaitForRunStop(ctx context.Context, runID string) (int32, error) {
	resp, err := c.waitForHistoryEvent(ctx, &pb.WaitForHistoryEventRequest{
		Namespace: c.namespace,
		RunId:     runID,
		Condition: &pb.WaitForHistoryEventRequest_UntilRunStop{UntilRunStop: true},
	})
	if err != nil {
		return 0, err
	}
	return resp.RunStatus, nil
}

// waitForHistoryEvent issues the long-poll RPC with transient-error retry.
// callWithRetry stops retrying the moment ctx is done, so a caller-deadline
// DeadlineExceeded returns promptly instead of looping.
func (c *RawClient) waitForHistoryEvent(ctx context.Context, req *pb.WaitForHistoryEventRequest) (*pb.WaitForHistoryEventResponse, error) {
	return callWithRetry(ctx, func(ctx context.Context) (*pb.WaitForHistoryEventResponse, error) {
		return c.runsClient.WaitForHistoryEvent(ctx, req)
	})
}
