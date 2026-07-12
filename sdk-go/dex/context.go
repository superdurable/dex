package dex

import (
	"context"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
)

// Context carries workflow execution metadata and step-scoped state access.
type Context interface {
	context.Context
	RunID() string
	StepExecutionID() string
	// FromStepExecutionID is the parent step exe id that spawned this step via NextSteps.
	FromStepExecutionID() string
	GetShutdownChannel() <-chan struct{}
	// TimerFired is true when Execute resumes because a durable timer fired.
	TimerFired() bool
}

type contextImpl struct {
	context.Context
	runID               string
	stepExecutionID     string
	fromStepExecutionID string
	shutdownCh          <-chan struct{}
	persistence         *persistenceImpl
	results             *conditionResults
}

func (ctx *contextImpl) RunID() string { return ctx.runID }
func (ctx *contextImpl) StepExecutionID() string {
	return ctx.stepExecutionID
}
func (ctx *contextImpl) FromStepExecutionID() string {
	return ctx.fromStepExecutionID
}
func (ctx *contextImpl) GetShutdownChannel() <-chan struct{} {
	return ctx.shutdownCh
}

func (ctx *contextImpl) TimerFired() bool {
	return ctx.results.timerFired
}

func newBaseContext(
	parent context.Context,
	runID, stepExecutionID, fromStepExecutionID string,
	shutdownCh <-chan struct{},
) Context {
	return &contextImpl{
		Context:             parent,
		runID:               runID,
		stepExecutionID:     stepExecutionID,
		fromStepExecutionID: fromStepExecutionID,
		shutdownCh:          shutdownCh,
		results:             &emptyConditionResults,
	}
}

func newStepContext(
	parent context.Context,
	runID, stepExecutionID, fromStepExecutionID string,
	shutdownCh <-chan struct{},
	stateMap map[string]*pb.Value,
	schema PersistenceSchema,
	results *conditionResults,
	codec ObjectCodec,
) *contextImpl {
	return &contextImpl{
		Context:             parent,
		runID:               runID,
		stepExecutionID:     stepExecutionID,
		fromStepExecutionID: fromStepExecutionID,
		shutdownCh:          shutdownCh,
		persistence:         newPersistence(stateMap, schema, codec),
		results:             results,
	}
}

type conditionResults struct {
	timerFired      bool
	channelMessages map[string][]*pb.Value
}

var emptyConditionResults conditionResults

func newConditionResults(timerFired bool, channelMessages map[string][]*pb.Value) *conditionResults {
	if !timerFired && len(channelMessages) == 0 {
		return &emptyConditionResults
	}
	return &conditionResults{
		timerFired:      timerFired,
		channelMessages: channelMessages,
	}
}

func (results *conditionResults) GetChannelMessages(channelName string) []*pb.Value {
	if results == nil || results.channelMessages == nil {
		return nil
	}
	values, ok := results.channelMessages[channelName]
	if !ok || len(values) == 0 {
		return nil
	}
	return values
}

func asStepContext(ctx Context) (*contextImpl, error) {
	impl, ok := ctx.(*contextImpl)
	if !ok {
		return nil, newInvalidContextError(ctx)
	}
	return impl, nil
}

// NewTestContext builds a step Context for unit tests outside the worker.
func NewTestContext(
	parent context.Context,
	schema PersistenceSchema,
	stateMap map[string]*pb.Value,
	timerFired bool,
	channelMessages map[string][]*pb.Value,
) Context {
	if parent == nil {
		parent = context.Background()
	}
	return newStepContext(
		parent,
		"test-run",
		"test-step",
		"",
		nil,
		stateMap,
		schema,
		newConditionResults(timerFired, channelMessages),
		DefaultObjectCodec(),
	)
}
