// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type registrationInput struct {
	Value string
}

type registrationOutput struct {
	Value string
}

type automaticRegistrationStep struct {
	StepDefaultsNoWaitFor[registrationInput]
}

func (automaticRegistrationStep) Execute(
	Context,
	registrationInput,
) (*StepDecision, error) {
	return DeadEnd(), nil
}

type stepDefaultsWithoutWaitFor struct {
	StepDefaults
}

func (stepDefaultsWithoutWaitFor) Execute(
	Context,
	registrationInput,
) (*StepDecision, error) {
	return DeadEnd(), nil
}

type automaticRegistrationFlow struct {
	FlowDefaults
}

func (automaticRegistrationFlow) GetSteps() []StepDef {
	return []StepDef{DefineStartStep(automaticRegistrationStep{})}
}

func (automaticRegistrationFlow) GetPersistenceSchema() PersistenceSchema {
	return PersistenceSchema{}
}

type registrationStep struct {
	stepType     string
	options      *StepOptions
	waitForInput registrationInput
	executeInput registrationInput
}

func (step *registrationStep) GetStepType() string {
	return step.stepType
}

func (step *registrationStep) GetStepOptions() *StepOptions {
	return step.options
}

func (step *registrationStep) WaitFor(
	_ Context,
	input registrationInput,
) (*Wait, error) {
	step.waitForInput = input
	return SkipWaitImmediately(), nil
}

func (step *registrationStep) Execute(
	_ Context,
	input registrationInput,
) (*StepDecision, error) {
	step.executeInput = input
	return DeadEnd(), nil
}

type executeOnlyRegistrationStep struct {
	StepDefaultsNoWaitFor[registrationInput]
	stepType string
}

func (step *executeOnlyRegistrationStep) GetStepType() string {
	return step.stepType
}

func (*executeOnlyRegistrationStep) Execute(
	Context,
	registrationInput,
) (*StepDecision, error) {
	return DeadEnd(), nil
}

type stringRegistrationStep struct {
	StepDefaultsNoWaitFor[string]
	stepType string
}

func (step *stringRegistrationStep) GetStepType() string {
	return step.stepType
}

func (*stringRegistrationStep) Execute(
	Context,
	string,
) (*StepDecision, error) {
	return DeadEnd(), nil
}

type registrationFlow struct {
	flowType string
	steps    []StepDef
	schema   PersistenceSchema
	rpcCalls int
}

func (flow *registrationFlow) GetFlowType() string {
	return flow.flowType
}

func (flow *registrationFlow) GetSteps() []StepDef {
	return flow.steps
}

func (flow *registrationFlow) GetPersistenceSchema() PersistenceSchema {
	return flow.schema
}

func (flow *registrationFlow) Update(
	_ Context,
	input registrationInput,
) (*RPCResult[registrationOutput], error) {
	flow.rpcCalls++
	return &RPCResult[registrationOutput]{Output: registrationOutput{Value: input.Value}}, nil
}

type invalidRPCRegistrationFlow struct {
	flowType string
}

func (flow invalidRPCRegistrationFlow) GetFlowType() string {
	return flow.flowType
}

func (invalidRPCRegistrationFlow) GetSteps() []StepDef {
	return nil
}

func (invalidRPCRegistrationFlow) GetPersistenceSchema() PersistenceSchema {
	return PersistenceSchema{}
}

func (invalidRPCRegistrationFlow) ExportedHelper() string {
	return "helper"
}

func (invalidRPCRegistrationFlow) WrongResult(
	Context,
	registrationInput,
) (registrationOutput, error) {
	return registrationOutput{}, nil
}

func (invalidRPCRegistrationFlow) ValueResult(
	Context,
	registrationInput,
) (RPCResult[registrationOutput], error) {
	return RPCResult[registrationOutput]{}, nil
}

func (invalidRPCRegistrationFlow) Update(
	Context,
	registrationInput,
) (*RPCResult[registrationOutput], error) {
	return &RPCResult[registrationOutput]{}, nil
}

type valueRegistrationFlow struct{}

func (valueRegistrationFlow) GetFlowType() string {
	return "value-flow"
}

func (valueRegistrationFlow) GetSteps() []StepDef {
	return nil
}

func (valueRegistrationFlow) GetPersistenceSchema() PersistenceSchema {
	return PersistenceSchema{}
}

func (valueRegistrationFlow) Query(
	Context,
	registrationInput,
) (*RPCResult[registrationOutput], error) {
	return &RPCResult[registrationOutput]{Output: registrationOutput{Value: "value"}}, nil
}

type mixedReceiverRegistrationFlow struct{}

func (mixedReceiverRegistrationFlow) GetFlowType() string {
	return "mixed-flow"
}

func (mixedReceiverRegistrationFlow) GetSteps() []StepDef {
	return nil
}

func (mixedReceiverRegistrationFlow) GetPersistenceSchema() PersistenceSchema {
	return PersistenceSchema{}
}

func (*mixedReceiverRegistrationFlow) Update(
	Context,
	registrationInput,
) (*RPCResult[registrationOutput], error) {
	return &RPCResult[registrationOutput]{}, nil
}

type errorRegistrationFlow struct {
	rpcError error
}

func (errorRegistrationFlow) GetFlowType() string {
	return "error-flow"
}

func (errorRegistrationFlow) GetSteps() []StepDef {
	return nil
}

func (errorRegistrationFlow) GetPersistenceSchema() PersistenceSchema {
	return PersistenceSchema{}
}

func (flow errorRegistrationFlow) Fail(
	Context,
	registrationInput,
) (*RPCResult[registrationOutput], error) {
	return nil, flow.rpcError
}

type registrationContext struct {
	context.Context
}

func (registrationContext) FlowID() string {
	return "flow-id"
}

func (registrationContext) RunID() string {
	return "run-id"
}

func (registrationContext) FlowStartedAt() time.Time {
	return time.Time{}
}

func (registrationContext) StepExecutionID() string {
	return "step-execution-id"
}

func (registrationContext) FromStepExecutionID() string {
	return ""
}

func (registrationContext) FirstAttemptAt() time.Time {
	return time.Time{}
}

func (registrationContext) Attempt() int32 {
	return 1
}

func (registrationContext) HasTimerFired() bool {
	return false
}

func (registrationContext) HasTimerFiredByIndex(int) bool {
	return false
}

func (registrationContext) WaitForMethodFailed() bool {
	return false
}

func (registrationContext) SetStepExecutionLocal(string, any) error {
	return nil
}

func (registrationContext) GetStepExecutionLocal(
	string,
	any,
) (bool, error) {
	return false, nil
}

func (registrationContext) RecordEvent(string, any) error {
	return nil
}

func TestRegistryAssemblesScopedDefinitions(t *testing.T) {
	start := &registrationStep{stepType: "start"}
	executeOnly := &executeOnlyRegistrationStep{stepType: "execute-only"}
	status := DefineAttribute[string](
		"status",
		Indexed(AttributeIndex{Type: IndexKeyword}),
	)
	commands := DefineChannel[registrationInput]("commands")
	progress := DefineStream[string]("progress", 1<<20)
	first := &registrationFlow{
		flowType: "first",
		steps: []StepDef{
			DefineStartStep(start),
			DefineStep(executeOnly),
		},
		schema: PersistenceSchema{
			Attributes: []AttributeDef{status},
			Channels:   []ChannelDef{commands},
			Streams:    []StreamDef{progress},
		},
	}
	second := &registrationFlow{
		flowType: "second",
		steps:    []StepDef{DefineStep(&registrationStep{stepType: "start"})},
	}

	assembled, err := NewRegistry([]Flow{first, second})
	require.NoError(t, err)

	firstRegistration, found := assembled.lookupFlow("first")
	require.True(t, found)
	require.Equal(t, "start", firstRegistration.startingStep.stepType)
	require.Len(t, firstRegistration.steps, 2)
	require.Len(t, firstRegistration.attributes, 1)
	require.Len(t, firstRegistration.channels, 1)
	require.Len(t, firstRegistration.streams, 1)
	require.False(t, firstRegistration.steps["start"].skipWaitFor)
	require.True(t, firstRegistration.steps["execute-only"].skipWaitFor)
	_, found = firstRegistration.lookupRPC("Update")
	require.True(t, found)

	secondRegistration, found := assembled.lookupFlow("second")
	require.True(t, found)
	_, found = secondRegistration.lookupStep("start")
	require.True(t, found)
}

func TestRegistryRejectsNonRPCExportedMethods(t *testing.T) {
	assembled, err := NewRegistry([]Flow{
		invalidRPCRegistrationFlow{flowType: "invalid-rpc"},
	})
	require.Nil(t, assembled)
	require.ErrorContains(t, err, "exported methods")
	require.ErrorContains(t, err, "ExportedHelper")
	require.ErrorContains(t, err, "ValueResult")
	require.ErrorContains(t, err, "WrongResult")
	require.ErrorContains(t, err, "must be RPCs")
}

func TestRegistryUsesDefaultPackageQualifiedTypes(t *testing.T) {
	flow := automaticRegistrationFlow{}
	step := automaticRegistrationStep{}
	require.Equal(t, "dex.automaticRegistrationFlow", GetFinalFlowType(flow))
	require.Equal(t, GetFinalFlowType(flow), GetFinalFlowType(&flow))
	require.Equal(t, "dex.automaticRegistrationStep", GetFinalStepType(step))
	require.Equal(t, GetFinalStepType(step), GetFinalStepType(&step))

	registry, err := NewRegistry([]Flow{&flow})
	require.NoError(t, err)
	registered, found := registry.lookupFlow("dex.automaticRegistrationFlow")
	require.True(t, found)
	require.Equal(t, "dex.automaticRegistrationStep", registered.startingStep.stepType)
}

func TestStepDefaultsRequiresWaitFor(t *testing.T) {
	_, implementsStep := any(stepDefaultsWithoutWaitFor{}).(Step[registrationInput])
	require.False(t, implementsStep)
	_, implementsStep = any(automaticRegistrationStep{}).(Step[registrationInput])
	require.True(t, implementsStep)
}

func TestRegistryRejectsInvalidFlowsAndSteps(t *testing.T) {
	var nilFlow *registrationFlow
	var nilStep *registrationStep
	validStep := &registrationStep{stepType: "step"}

	tests := []struct {
		name  string
		flows []Flow
		error string
	}{
		{
			name:  "nil flow",
			flows: []Flow{nil},
			error: "flow at index 0 is nil",
		},
		{
			name:  "typed nil flow",
			flows: []Flow{nilFlow},
			error: "flow at index 0 is nil",
		},
		{
			name: "duplicate flow type",
			flows: []Flow{
				&registrationFlow{flowType: "flow"},
				&registrationFlow{flowType: "flow"},
			},
			error: "duplicate flow type",
		},
		{
			name: "zero step definition",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				steps:    []StepDef{nil},
			}},
			error: "step at index 0 is nil",
		},
		{
			name: "typed nil step",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				steps:    []StepDef{DefineStep(nilStep)},
			}},
			error: "step at index 0 is nil",
		},
		{
			name: "duplicate step type",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				steps: []StepDef{
					DefineStep(validStep),
					DefineStep(&registrationStep{stepType: "step"}),
				},
			}},
			error: "duplicate step type",
		},
		{
			name: "multiple starting steps",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				steps: []StepDef{
					DefineStartStep(validStep),
					DefineStartStep(
						&registrationStep{stepType: "second"},
					),
				},
			}},
			error: "multiple starting steps",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			assembled, err := NewRegistry(testCase.flows)
			require.Nil(t, assembled)
			require.ErrorContains(t, err, testCase.error)
		})
	}
}

func TestRegistryValidatesPersistenceSchema(t *testing.T) {
	keyword := DefineAttribute[string](
		"keyword",
		Indexed(AttributeIndex{
			Type:     IndexKeyword,
			IndexKey: "shared",
		}),
	)
	integer := DefineAttribute[int64](
		"integer",
		Indexed(AttributeIndex{
			Type:     IndexInt,
			IndexKey: "shared",
		}),
	)
	sharedStream := DefineStream[string]("shared", 1<<20)

	tests := []struct {
		name  string
		flows []Flow
		error string
	}{
		{
			name: "nil attribute",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				schema: PersistenceSchema{
					Attributes: []AttributeDef{nil},
				},
			}},
			error: "attribute at index 0 is nil",
		},
		{
			name: "empty attribute",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				schema: PersistenceSchema{
					Attributes: []AttributeDef{
						DefineAttribute[string](""),
					},
				},
			}},
			error: "attribute name must not be empty",
		},
		{
			name: "duplicate attribute",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				schema: PersistenceSchema{
					Attributes: []AttributeDef{
						DefineAttribute[string]("same"),
						DefineAttributeMap[int]("same"),
					},
				},
			}},
			error: "duplicate attribute",
		},
		{
			name: "invalid index type",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				schema: PersistenceSchema{
					Attributes: []AttributeDef{
						DefineAttribute[string](
							"invalid",
							Indexed(AttributeIndex{Type: IndexType(99)}),
						),
					},
				},
			}},
			error: "unsupported index type",
		},
		{
			name: "nil channel",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				schema: PersistenceSchema{
					Channels: []ChannelDef{nil},
				},
			}},
			error: "channel at index 0 is nil",
		},
		{
			name: "empty channel",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				schema: PersistenceSchema{
					Channels: []ChannelDef{
						DefineChannel[string](""),
					},
				},
			}},
			error: "channel name must not be empty",
		},
		{
			name: "duplicate channel",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				schema: PersistenceSchema{
					Channels: []ChannelDef{
						DefineChannel[string]("same"),
						DefineChannelMap[int]("same"),
					},
				},
			}},
			error: "duplicate channel",
		},
		{
			name: "nil stream",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				schema: PersistenceSchema{
					Streams: []StreamDef{nil},
				},
			}},
			error: "stream at index 0 is nil",
		},
		{
			name: "empty stream",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				schema: PersistenceSchema{
					Streams: []StreamDef{DefineStream[string]("", 1)},
				},
			}},
			error: "stream name must not be empty",
		},
		{
			name: "invalid stream capacity",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				schema: PersistenceSchema{
					Streams: []StreamDef{DefineStream[string]("stream", 0)},
				},
			}},
			error: "capacity bytes must be positive",
		},
		{
			name: "duplicate stream",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				schema: PersistenceSchema{
					Streams: []StreamDef{
						DefineStream[string]("same", 1),
						DefineStream[int]("same", 2),
					},
				},
			}},
			error: "duplicate stream",
		},
		{
			name: "stream registered by multiple flows",
			flows: []Flow{
				&registrationFlow{
					flowType: "first",
					schema:   PersistenceSchema{Streams: []StreamDef{sharedStream}},
				},
				&registrationFlow{
					flowType: "second",
					schema:   PersistenceSchema{Streams: []StreamDef{sharedStream}},
				},
			},
			error: "already registered by flow",
		},
		{
			name: "shared index conflict",
			flows: []Flow{
				&registrationFlow{
					flowType: "first",
					schema: PersistenceSchema{
						Attributes: []AttributeDef{keyword},
					},
				},
				&registrationFlow{
					flowType: "second",
					schema: PersistenceSchema{
						Attributes: []AttributeDef{integer},
					},
				},
			},
			error: "index key \"shared\" has conflicting types",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			assembled, err := NewRegistry(testCase.flows)
			require.Nil(t, assembled)
			require.ErrorContains(t, err, testCase.error)
		})
	}
}

func TestRegistryValidatesStepOptions(t *testing.T) {
	status := DefineAttribute[string]("status")
	items := DefineAttributeMap[int]("items")
	target := &registrationStep{stepType: "target"}
	stringTarget := &stringRegistrationStep{stepType: "string-target"}
	unregistered := &registrationStep{stepType: "missing"}

	cycle := &StepOptions{}
	cycle.ExecuteFailure = ProceedToOnExecuteFailure(target, cycle)

	tests := []struct {
		name    string
		options *StepOptions
		steps   []StepDef
		error   string
	}{
		{
			name: "undeclared lock",
			options: &StepOptions{
				ExecuteLockAttributes: []AttributeLock{
					LockAttribute(DefineAttribute[string]("missing")),
				},
			},
			error: "attribute \"missing\" is not declared",
		},
		{
			name: "wrong lock kind",
			options: &StepOptions{
				ExecuteLockAttributes: []AttributeLock{
					LockAttributeMap(
						DefineAttributeMap[string]("status"),
						"one",
					),
				},
			},
			error: "static/map kind does not match",
		},
		{
			name: "empty map instance",
			options: &StepOptions{
				ExecuteLockAttributes: []AttributeLock{
					LockAttributeMap(items, ""),
				},
			},
			error: "map instance must not be empty",
		},
		{
			name: "unregistered fallback",
			options: &StepOptions{
				ExecuteFailure: ProceedToOnExecuteFailure(
					unregistered,
					nil,
				),
			},
			error: "step \"missing\" is not registered",
		},
		{
			name: "fallback input mismatch",
			options: &StepOptions{
				ExecuteFailure: ProceedToOnExecuteFailure(
					stringTarget,
					nil,
				),
			},
			steps: []StepDef{DefineStep(stringTarget)},
			error: "does not match",
		},
		{
			name:    "fallback cycle",
			options: cycle,
			error:   "step options contain a cycle",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			source := &registrationStep{
				stepType: "source",
				options:  testCase.options,
			}
			steps := []StepDef{
				DefineStep(source),
				DefineStep(target),
			}
			steps = append(steps, testCase.steps...)
			assembled, err := NewRegistry([]Flow{&registrationFlow{
				flowType: "flow",
				steps:    steps,
				schema: PersistenceSchema{
					Attributes: []AttributeDef{status, items},
				},
			}})
			require.Nil(t, assembled)
			require.ErrorContains(t, err, testCase.error)
		})
	}
}

func TestStepAdaptersAndRuntimeReferences(t *testing.T) {
	start := &registrationStep{stepType: "start"}
	registeredTarget := &registrationStep{stepType: "target"}
	commands := DefineChannel[registrationInput]("commands")
	flow := &registrationFlow{
		flowType: "flow",
		steps: []StepDef{
			DefineStartStep(start),
			DefineStep(registeredTarget),
		},
		schema: PersistenceSchema{
			Channels: []ChannelDef{commands},
		},
	}
	assembled, err := NewRegistry([]Flow{flow})
	require.NoError(t, err)
	registeredFlow, found := assembled.lookupFlow("flow")
	require.True(t, found)

	ctx := registrationContext{Context: context.Background()}
	input := registrationInput{Value: "input"}
	step, found := registeredFlow.lookupStep("start")
	require.True(t, found)
	_, err = step.handler.waitFor(ctx, input)
	require.NoError(t, err)
	_, err = step.handler.execute(ctx, input)
	require.NoError(t, err)
	require.Equal(t, input, start.waitForInput)
	require.Equal(t, input, start.executeInput)
	_, err = step.handler.execute(ctx, "wrong")
	require.ErrorContains(t, err, "not assignable")

	lookalike := &registrationStep{
		stepType: "target",
		options: &StepOptions{
			ExecuteMethodTimeout: time.Hour,
		},
	}
	resolved, err := registeredFlow.resolveMovement(
		MovementOf(lookalike, input),
	)
	require.NoError(t, err)
	require.Same(t, registeredTarget, resolved.handler.stepValue())
	require.Nil(t, resolved.options)

	_, err = registeredFlow.resolveMovement(StepMovement{
		step:  typedStepDef[registrationInput]{step: lookalike},
		input: "wrong",
	})
	require.ErrorContains(t, err, "not assignable")
	_, err = registeredFlow.resolveMovement(
		MovementOf(&registrationStep{stepType: "missing"}, input),
	)
	require.ErrorContains(t, err, "is not registered")

	_, err = registeredFlow.resolveChannels([]ChannelDef{commands, commands})
	require.ErrorContains(t, err, "duplicate channel reference")
	_, err = registeredFlow.resolveChannels([]ChannelDef{
		DefineChannelMap[registrationInput]("commands"),
	})
	require.ErrorContains(t, err, "static/map kind")
	_, err = registeredFlow.resolveChannels([]ChannelDef{
		DefineChannel[registrationInput]("missing"),
	})
	require.ErrorContains(t, err, "is not declared")
}

func TestRegistryReportsFirstInvalidStepOptions(t *testing.T) {
	first := &registrationStep{
		stepType: "aaa",
		options: &StepOptions{
			WaitForLockAttributes: []AttributeLock{
				LockAttribute(DefineAttribute[string]("undeclared-a")),
			},
		},
	}
	second := &registrationStep{
		stepType: "zzz",
		options: &StepOptions{
			WaitForLockAttributes: []AttributeLock{
				LockAttribute(DefineAttribute[string]("undeclared-z")),
			},
		},
	}
	for attempt := 0; attempt < 50; attempt++ {
		assembled, err := NewRegistry([]Flow{&registrationFlow{
			flowType: "probe",
			steps: []StepDef{
				DefineStep(first),
				DefineStep(second),
			},
		}})
		require.Nil(t, assembled)
		require.ErrorContains(t, err, `step "aaa" options`)
		require.NotContains(t, err.Error(), `step "zzz" options`)
	}
}

func TestRegistryRejectsPointerOnlyRPCsOnValueFlow(t *testing.T) {
	assembled, err := NewRegistry([]Flow{mixedReceiverRegistrationFlow{}})
	require.Nil(t, assembled)
	require.ErrorContains(t, err, `exported methods [Update]`)
	require.ErrorContains(t, err, "pointer receivers")
	require.ErrorContains(t, err, "register *mixedReceiverRegistrationFlow")

	name, err := rpcMethodName((&mixedReceiverRegistrationFlow{}).Update)
	require.NoError(t, err)
	require.Equal(t, "Update", name)
}

func TestRPCDiscoveryInvocationAndIdentity(t *testing.T) {
	pointerFlow := &registrationFlow{flowType: "pointer-flow"}
	assembled, err := NewRegistry([]Flow{
		pointerFlow,
		valueRegistrationFlow{},
	})
	require.NoError(t, err)

	registeredPointerFlow, found := assembled.lookupFlow("pointer-flow")
	require.True(t, found)
	update, found := registeredPointerFlow.lookupRPC("Update")
	require.True(t, found)
	require.Equal(t, reflect.TypeFor[registrationInput](), update.input)
	require.Equal(t, reflect.TypeFor[registrationOutput](), update.output)

	ctx := registrationContext{Context: context.Background()}
	result, err := update.invoke(ctx, registrationInput{Value: "updated"})
	require.NoError(t, err)
	require.Equal(
		t,
		registrationOutput{Value: "updated"},
		result.rpcOutput(),
	)
	require.Equal(t, 1, pointerFlow.rpcCalls)
	_, err = update.invoke(ctx, "wrong")
	require.ErrorContains(t, err, "not assignable")

	registeredValueFlow, found := assembled.lookupFlow("value-flow")
	require.True(t, found)
	_, found = registeredValueFlow.lookupRPC("Query")
	require.True(t, found)
	name, err := rpcMethodName(valueRegistrationFlow{}.Query)
	require.NoError(t, err)
	require.Equal(t, "Query", name)

	name, err = rpcMethodName(pointerFlow.Update)
	require.NoError(t, err)
	require.Equal(t, "Update", name)

	_, err = rpcMethodName(packageRegistrationRPC)
	require.ErrorContains(t, err, "direct bound Flow method")
	_, err = rpcMethodName((*registrationFlow).Update)
	require.ErrorContains(t, err, "direct bound Flow method")
	wrapper := func(
		ctx Context,
		input registrationInput,
	) (*RPCResult[registrationOutput], error) {
		return pointerFlow.Update(ctx, input)
	}
	_, err = rpcMethodName(wrapper)
	require.ErrorContains(t, err, "direct bound Flow method")
}

func TestRPCInvocationReturnsApplicationError(t *testing.T) {
	expected := errors.New("failed")
	flow := errorRegistrationFlow{rpcError: expected}
	assembled, err := NewRegistry([]Flow{flow})
	require.NoError(t, err)
	registered, found := assembled.lookupFlow("error-flow")
	require.True(t, found)
	rpc, found := registered.lookupRPC("Fail")
	require.True(t, found)

	_, err = rpc.invoke(
		registrationContext{Context: context.Background()},
		registrationInput{},
	)
	require.ErrorIs(t, err, expected)
}

func packageRegistrationRPC(
	Context,
	registrationInput,
) (*RPCResult[registrationOutput], error) {
	return &RPCResult[registrationOutput]{}, nil
}
