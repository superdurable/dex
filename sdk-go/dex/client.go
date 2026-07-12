package dex

import (
	"context"
	"fmt"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"google.golang.org/grpc"
)

// Run status constants matching the server's persistence layer.
const (
	RunStatusPending                      int32 = 0
	RunStatusWaitingForWorker             int32 = 1
	RunStatusRunning                      int32 = 2
	RunStatusAllStepsWaitingForConditions int32 = 3
	RunStatusCompleted                    int32 = 4
	RunStatusFailed                       int32 = 5
)

// StopRun target statuses accepted by Client.StopRun / RawClient.StopRun.
const (
	StopRunComplete = pb.StopDecision_STOP_DECISION_COMPLETE
	StopRunFail     = pb.StopDecision_STOP_DECISION_FAIL
)

// DefaultTaskListName is the default tasklist applied when
// RunOptions.TaskListName is empty (or RunOptions itself is nil), and
// when WorkerOptions.TaskListName is empty. Same constant on both sides
// so a worker started with default options will pick up runs started
// with default options out of the box.
const DefaultTaskListName = "defaultTaskList"

// RunOptions configures StartRunWithOptions behavior.
type RunOptions struct {
	// TaskListName routes the run to a specific tasklist (the worker that
	// polls this tasklist will be dispatched the run). Empty (or nil
	// RunOptions) defaults to DefaultTaskListName — the same default
	// that NewWorker uses for WorkerOptions.TaskListName.
	TaskListName string
}

func (o *RunOptions) taskListName() string {
	if o != nil && o.TaskListName != "" {
		return o.TaskListName
	}
	return DefaultTaskListName
}

// Client is a typed gRPC client that wraps RawClient and uses a Registry
// for flow type resolution and state type safety.
type Client struct {
	raw      *RawClient
	registry *Registry
}

// NewClient creates a typed Client backed by the given registry.
func NewClient(registry *Registry, runsConn grpc.ClientConnInterface, namespace string) *Client {
	return &Client{
		raw:      NewRawClient(runsConn, namespace, registry.ObjectCodec()),
		registry: registry,
	}
}

// StartRun starts a new flow run with default options (DefaultTaskListName).
// See StartRunWithOptions for input semantics and RunOptions.
func (client *Client) StartRun(ctx context.Context, runID string, flow Flow, inputs ...any) error {
	return client.StartRunWithOptions(ctx, runID, flow, nil, inputs...)
}

// StartRunWithOptions starts a new flow run. The flow type is derived from the
// registered Flow type parameter, and the set of starting steps is taken
// from the registry's StartingStep[...] entries (preserving registration
// order).
//
// inputs MUST be either:
//   - empty (no inputs): every starting step receives a nil input. Use
//     this when the flow's starting steps don't take meaningful input
//     (e.g. a dispatch step that reads from external state).
//   - exactly N (N == number of starting steps): per-step mapping;
//
// inputs[i] must match the i-th starting step's input type (the IN type
// parameter on Step.Execute). A nil input is allowed and encodes as null.
//
// Any other length is an explicit error — there's no broadcast
// convenience because in real flows that take per-step inputs, the
// "broadcast same value to N steps" case is almost always a bug
// (silently sending the wrong payload to N-1 steps), so we'd rather
// fail loudly. For a single starting step, "1 input" already satisfies
// the N == count rule.
//
// options may be nil for all defaults; see RunOptions.
func (client *Client) StartRunWithOptions(ctx context.Context, runID string, flow Flow, options *RunOptions, inputs ...any) error {
	flowType := GetFinalFlowType(flow)
	reg, ok := client.registry.getFlowRegistration(flowType)
	if !ok {
		return newFlowNotRegisteredError(flowType)
	}

	startingSteps, err := buildStartingSteps(*reg, client.registry.ObjectCodec(), inputs)
	if err != nil {
		return err
	}

	return client.raw.StartRun(ctx, runID, flowType, options.taskListName(), startingSteps)
}

// GetRun retrieves the raw run result (untyped state).
func (client *Client) GetRun(ctx context.Context, runID string) (*RunResult, error) {
	return client.raw.GetRun(ctx, runID)
}

// StopRun asks the server to stop the run with the given terminal outcome. See
// RawClient.StopRun for the full error semantics. reason is optional user text
// stored on the RunStop history event. Stopping an already-terminal run is
// idempotent; codes.NotFound is authoritative.
func (client *Client) StopRun(ctx context.Context, runID string, stopDecision pb.StopDecision, reason string) error {
	return client.raw.StopRun(ctx, runID, stopDecision, reason)
}

// PublishToChannel publishes one or more values to a static channel on a
// running run. Pass the channel wire name (e.g. myChannel.Name).
//
// Thin pass-through to RawClient.PublishToChannel; see that method's doc
// for delivery semantics (durable persist + best-effort matching push to
// the active worker stream).
func (client *Client) PublishToChannel(ctx context.Context, runID, channelName string, values ...any) error {
	return client.raw.PublishToChannel(ctx, runID, channelName, values...)
}

// PublishToChannelByName is an alias for PublishToChannel for callers
// that only have a raw channel name string.
func (client *Client) PublishToChannelByName(ctx context.Context, runID, channelName string, values ...any) error {
	return client.PublishToChannel(ctx, runID, channelName, values...)
}

// PublishToDynamicChannel publishes values to a dynamic channel instance.
// Wire name is prefix + key.
func (client *Client) PublishToDynamicChannel(ctx context.Context, runID, prefix, key string, values ...any) error {
	return client.PublishToChannel(ctx, runID, dynamicChannelName(prefix, key), values...)
}

// WaitForHistoryEvent long-polls until the run's latest history event id reaches
// expectedEventID (or the run closes) and returns that latest event id.
func (client *Client) WaitForHistoryEvent(ctx context.Context, runID string, expectedEventID int64) (int64, error) {
	return client.raw.WaitForHistoryEvent(ctx, runID, expectedEventID)
}

// WaitForRunComplete blocks until the run reaches a terminal state and returns
// its terminal status (RunStatusCompleted or RunStatusFailed).
func (client *Client) WaitForRunComplete(ctx context.Context, runID string) (int32, error) {
	return client.raw.WaitForRunStop(ctx, runID)
}

// buildStartingSteps resolves StartRun's variadic inputs (per-step,
// broadcast, or none) into one NextStep per registered starting step.
// Encoding happens per-step so a per-step JSON failure surfaces with the
// owning step ID in the error message rather than swallowing the index.
func buildStartingSteps(reg flowRegistration, codec ObjectCodec, inputs []any) ([]*pb.NextStep, error) {
	n := len(reg.startingSteps)
	if n == 0 {
		return nil, ErrNoStartingSteps
	}

	perStep := make([]any, n)
	switch len(inputs) {
	case 0:
		// perStep already nil-filled — every starting step gets a nil input.
	case n:
		copy(perStep, inputs)
	default:
		return nil, newInputCountMismatchError(len(inputs), n)
	}

	steps := make([]*pb.NextStep, 0, n)
	for index, stepID := range reg.startingSteps {
		step, ok := reg.steps[stepID]
		if !ok {
			continue
		}
		inputType, err := stepInputType(step)
		if err != nil {
			return nil, fmt.Errorf("starting step %q (index %d): %w", stepID, index, err)
		}
		if err := validateStartingStepInput(stepID, index, perStep[index], inputType); err != nil {
			return nil, err
		}
		inputVal, err := codec.EncodeValue(perStep[index])
		if err != nil {
			return nil, fmt.Errorf("encode input for starting step %q (index %d): %w", stepID, index, err)
		}
		steps = append(steps, &pb.NextStep{
			StepId:              stepID,
			Input:               inputVal,
			SkipWaitFor:         ShouldSkipWaitFor(step),
			StepOptionsSnapshot: stepOptionsToSnapshot(step),
		})
	}
	return steps, nil
}
