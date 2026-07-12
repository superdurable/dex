package dex

// TerminalType identifies how a workflow ends.
type TerminalType int

const (
	TerminalComplete TerminalType = iota
	TerminalFail
	TerminalDeadEnd
)

// TerminalDecision represents a workflow-ending outcome.
type TerminalDecision struct {
	Type   TerminalType
	Output any
}

// StepMovement describes a transition to a next step.
type StepMovement struct {
	StepID string
	Input  any
}

// ChannelMessage is one or more messages to publish to a channel as a side effect.
type ChannelMessage struct {
	ChannelName string
	Values      []any
}

// NewChannelMessage creates a type-safe ChannelMessage for a static channel.
func NewChannelMessage[T any](ch Channel[T], values ...T) ChannelMessage {
	anyValues := make([]any, len(values))
	for index, value := range values {
		anyValues[index] = value
	}
	return ChannelMessage{ChannelName: ch.Name, Values: anyValues}
}

// NewDynamicChannelMessage creates a type-safe ChannelMessage for a dynamic channel instance.
func NewDynamicChannelMessage[T any](dc DynamicChannel[T], key string, values ...T) ChannelMessage {
	anyValues := make([]any, len(values))
	for index, value := range values {
		anyValues[index] = value
	}
	return ChannelMessage{ChannelName: dynamicChannelName(dc.Prefix, key), Values: anyValues}
}

// StepDecision is the return value of Step.Execute.
type StepDecision interface {
	isStepDecision()
	WithCancelingSiblingStepExecution(refs ...CancelSiblingStepRef) StepDecision
}

type stepDecisionImpl struct {
	Movements            []StepMovement
	Terminal             *TerminalDecision
	CancelSiblingStepIDs []string
}

func (stepDecisionImpl) isStepDecision() {}

func stepDecisionMovements(decision StepDecision) []StepMovement {
	return decision.(*stepDecisionImpl).Movements
}

func stepDecisionTerminal(decision StepDecision) *TerminalDecision {
	return decision.(*stepDecisionImpl).Terminal
}

func stepDecisionCancelIDs(decision StepDecision) map[string]bool {
	ids := decision.(*stepDecisionImpl).CancelSiblingStepIDs
	m := make(map[string]bool)
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// CancelSiblingStepRef is an opaque reference to a step type.
type CancelSiblingStepRef struct {
	StepID string
}

// CancelOf builds a CancelSiblingStepRef for the given Step.
func CancelOf[IN any](step Step[IN]) CancelSiblingStepRef {
	return CancelSiblingStepRef{StepID: GetFinalStepId(step)}
}

func (decision *stepDecisionImpl) WithCancelingSiblingStepExecution(refs ...CancelSiblingStepRef) StepDecision {
	for _, ref := range refs {
		decision.CancelSiblingStepIDs = append(decision.CancelSiblingStepIDs, ref.StepID)
	}
	return decision
}

// GoTo creates a decision that transitions to a single next step.
func GoTo[IN any](step Step[IN], input IN) StepDecision {
	return &stepDecisionImpl{
		Movements: []StepMovement{{
			StepID: GetFinalStepId(step),
			Input:  input,
		}},
	}
}

// GoToMany creates a decision that fans out to multiple next steps.
func GoToMany(movements ...StepMovement) StepDecision {
	return &stepDecisionImpl{Movements: movements}
}

// MovementOf creates a single StepMovement, useful with GoToMany.
func MovementOf[IN any](step Step[IN], input IN) StepMovement {
	return StepMovement{
		StepID: GetFinalStepId(step),
		Input:  input,
	}
}

// Complete ends the workflow successfully with an optional output.
func Complete(output any) StepDecision {
	return &stepDecisionImpl{
		Terminal: &TerminalDecision{Type: TerminalComplete, Output: output},
	}
}

// Fail ends the workflow with a failure and optional output.
func Fail(output any) StepDecision {
	return &stepDecisionImpl{
		Terminal: &TerminalDecision{Type: TerminalFail, Output: output},
	}
}

// DeadEnd stops this execution branch without completing or failing the workflow.
func DeadEnd() StepDecision {
	return &stepDecisionImpl{
		Terminal: &TerminalDecision{Type: TerminalDeadEnd},
	}
}
