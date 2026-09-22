// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
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
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

type registrationInput struct {
	Value string
}

type registrationOutput struct {
	Value string
}

type registrationNamedStatus string

type registrationComplexStatus struct {
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
	rpcs     []RPCDef
	schema   PersistenceSchema
	rpcCalls int
}

func (flow *registrationFlow) GetFlowType() string {
	return flow.flowType
}

func (flow *registrationFlow) GetSteps() []StepDef {
	return flow.steps
}

func (flow *registrationFlow) GetRPCs() []RPCDef {
	return flow.rpcs
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

func (flow *registrationFlow) Query(
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

func (invalidRPCRegistrationFlow) GetRPCs() []RPCDef {
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

func (flow valueRegistrationFlow) GetRPCs() []RPCDef {
	return []RPCDef{DefineRPC(flow.Query, nil)}
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

func (flow mixedReceiverRegistrationFlow) GetRPCs() []RPCDef {
	return []RPCDef{DefineRPC((&flow).Update, nil)}
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

func (flow errorRegistrationFlow) GetRPCs() []RPCDef {
	return []RPCDef{DefineRPC(flow.Fail, nil)}
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

func (registrationContext) RecoveryError() *RecoveryErrorInfo {
	return nil
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

func (registrationContext) RecordHeartbeat(any) error {
	return nil
}

func (registrationContext) GetLastHeartbeatValue(any) (bool, error) {
	return false, nil
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
	first.rpcs = []RPCDef{DefineRPC(first.Update, nil)}
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

func TestRegistryIgnoresUnregisteredExportedMethods(t *testing.T) {
	assembled, err := NewRegistry([]Flow{
		invalidRPCRegistrationFlow{flowType: "invalid-rpc"},
	})
	require.NoError(t, err)
	registered, found := assembled.lookupFlow("invalid-rpc")
	require.True(t, found)
	require.Empty(t, registered.rpcs)
}

func TestRegistryRejectsInvalidRPCDefinitions(t *testing.T) {
	status := DefineAttribute[string]("status")
	missingStatus := DefineAttribute[string]("missing-status")
	items := DefineAttributeMap[int]("items")
	missingItems := DefineAttributeMap[int]("missing-items")

	tests := []struct {
		name        string
		definitions func(*registrationFlow) []RPCDef
		options     *RPCOptions
		error       string
	}{
		{
			name: "nil RPC",
			definitions: func(*registrationFlow) []RPCDef {
				var rpc RPC[registrationInput, registrationOutput]
				return []RPCDef{DefineRPC(rpc, nil)}
			},
			error: "RPC at index 0 is nil",
		},
		{
			name: "package function",
			definitions: func(*registrationFlow) []RPCDef {
				return []RPCDef{DefineRPC(packageRegistrationRPC, nil)}
			},
			error: "direct bound Flow method",
		},
		{
			name: "foreign Flow method",
			definitions: func(*registrationFlow) []RPCDef {
				foreign := valueRegistrationFlow{}
				return []RPCDef{DefineRPC(foreign.Query, nil)}
			},
			error: "must be a direct bound method",
		},
		{
			name: "duplicate RPC",
			definitions: func(flow *registrationFlow) []RPCDef {
				return []RPCDef{
					DefineRPC(flow.Update, nil),
					DefineRPC(flow.Update, nil),
				}
			},
			error: `duplicate RPC "Update"`,
		},
		{
			name:    "negative timeout",
			options: &RPCOptions{Timeout: -time.Second},
			error:   "duration must not be negative",
		},
		{
			name: "undeclared lock",
			options: &RPCOptions{
				LockAttributes: []AttributeLock{LockAttribute(missingStatus)},
			},
			error: `attribute "missing-status" is not declared`,
		},
		{
			name: "undeclared state load",
			options: &RPCOptions{
				LoadAttributeMaps: []AttributeDef{missingItems},
			},
			error: `AttributeMap "missing-items" is not registered`,
		},
		{
			name: "duplicate state load",
			options: &RPCOptions{
				LoadAttributeMaps: []AttributeDef{items, items},
			},
			error: `duplicate AttributeMap load "items/"`,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			flow := &registrationFlow{
				flowType: "rpc-options",
				schema: PersistenceSchema{
					Attributes: []AttributeDef{status, items},
				},
			}
			if testCase.definitions != nil {
				flow.rpcs = testCase.definitions(flow)
			} else {
				flow.rpcs = []RPCDef{DefineRPC(flow.Update, testCase.options)}
			}
			assembled, err := NewRegistry([]Flow{flow})
			require.Nil(t, assembled)
			require.ErrorContains(t, err, testCase.error)
		})
	}
}

func TestRegistryRegistersActionPermissionProjection(t *testing.T) {
	status := DefineAttribute[registrationNamedStatus]("status")
	flow := &registrationFlow{
		flowType: "action-projection",
		schema: PersistenceSchema{
			Attributes: []AttributeDef{status},
		},
	}
	flow.rpcs = []RPCDef{
		DefineRPC(flow.Update, &RPCOptions{Action: DefineAction(
			"Approve",
			WhenAttributeMatches(
				status,
				AttributeMatchEqual(registrationNamedStatus("A")),
				AttributeMatchEqual(registrationNamedStatus("B")),
			),
			ActionRequiresPermission("refund.approve"),
		)}),
		DefineRPC(flow.Query, &RPCOptions{Action: DefineAction(
			"Review",
			WhenAttributeMatches(
				status,
				AttributeMatchEqual(registrationNamedStatus("A")),
			),
			ActionRequiresPermission("refund.approve"),
		)}),
	}

	registry, err := NewRegistry([]Flow{flow})
	require.NoError(t, err)
	require.Equal(
		t,
		dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY,
		registry.attributeIndexes[WorkQueuePermissionsIndexKey],
	)
	require.Len(t, registry.flows[flow.flowType].actions, 2)
}

func TestRegistryRejectsInvalidActions(t *testing.T) {
	status := DefineAttribute[string]("status")
	missingStatus := DefineAttribute[string]("missing-status")
	items := DefineAttributeMap[int]("items")
	complexStatus := DefineAttribute[registrationComplexStatus]("complex-status")
	sliceStatus := DefineAttribute[[]string]("slice-status")
	mapStatus := DefineAttribute[map[string]string]("map-status")
	anyStatus := DefineAttribute[any]("any-status")

	var nilAction *actionImpl
	var nilCondition *attributeActionCondition[string]
	tests := []struct {
		name   string
		action ActionDef
		error  string
	}{
		{
			name:   "nil Action",
			action: nilAction,
			error:  "Action definition is nil",
		},
		{
			name: "empty label",
			action: DefineAction(
				"  ",
				WhenAttributeMatches(status, AttributeMatchEqual("A")),
				ActionRequiresPermission("permission-x"),
			),
			error: "Action label must not be empty",
		},
		{
			name: "nil condition",
			action: DefineAction(
				"Approve",
				nilCondition,
				ActionRequiresPermission("permission-x"),
			),
			error: "Action condition is nil",
		},
		{
			name: "missing permission",
			action: DefineAction(
				"Approve",
				WhenAttributeMatches(status, AttributeMatchEqual("A")),
			),
			error: "Action requires exactly one permission",
		},
		{
			name: "duplicate permission",
			action: DefineAction(
				"Approve",
				WhenAttributeMatches(status, AttributeMatchEqual("A")),
				ActionRequiresPermission("permission-x"),
				ActionRequiresPermission("permission-y"),
			),
			error: "Action requires exactly one permission",
		},
		{
			name: "no matches",
			action: DefineAction(
				"Approve",
				WhenAttributeMatches(status),
				ActionRequiresPermission("permission-x"),
			),
			error: "requires at least one match",
		},
		{
			name: "not equal",
			action: DefineAction(
				"Approve",
				WhenAttributeMatches(status, AttributeMatchNotEqual("A")),
				ActionRequiresPermission("permission-x"),
			),
			error: "supports only AttributeMatchEqual",
		},
		{
			name: "ordering operator",
			action: DefineAction(
				"Approve",
				WhenAttributeMatches(status, AttributeMatchGreaterThan("A")),
				ActionRequiresPermission("permission-x"),
			),
			error: "supports only AttributeMatchEqual",
		},
		{
			name: "unregistered source",
			action: DefineAction(
				"Approve",
				WhenAttributeMatches(missingStatus, AttributeMatchEqual("A")),
				ActionRequiresPermission("permission-x"),
			),
			error: `Attribute "missing-status" is not declared`,
		},
		{
			name: "AttributeMap source",
			action: DefineAction(
				"Approve",
				WhenAttributeMatches(
					DefineAttribute[int](items.AttributeName()),
					AttributeMatchEqual(1),
				),
				ActionRequiresPermission("permission-x"),
			),
			error: `Attribute "items" is not declared`,
		},
		{
			name: "struct source",
			action: DefineAction(
				"Approve",
				WhenAttributeMatches(
					complexStatus,
					AttributeMatchEqual(registrationComplexStatus{}),
				),
				ActionRequiresPermission("permission-x"),
			),
			error: "unsupported type",
		},
		{
			name: "slice source",
			action: DefineAction(
				"Approve",
				WhenAttributeMatches(sliceStatus, AttributeMatchEqual([]string{"A"})),
				ActionRequiresPermission("permission-x"),
			),
			error: "unsupported type",
		},
		{
			name: "map source",
			action: DefineAction(
				"Approve",
				WhenAttributeMatches(
					mapStatus,
					AttributeMatchEqual(map[string]string{"status": "A"}),
				),
				ActionRequiresPermission("permission-x"),
			),
			error: "unsupported type",
		},
		{
			name: "any source",
			action: DefineAction(
				"Approve",
				WhenAttributeMatches(anyStatus, AttributeMatchEqual[any]("A")),
				ActionRequiresPermission("permission-x"),
			),
			error: "unsupported type",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			flow := &registrationFlow{
				flowType: "invalid-action",
				schema: PersistenceSchema{Attributes: []AttributeDef{
					status,
					items,
					complexStatus,
					sliceStatus,
					mapStatus,
					anyStatus,
				}},
			}
			flow.rpcs = []RPCDef{DefineRPC(flow.Update, &RPCOptions{Action: testCase.action})}
			registry, err := NewRegistry([]Flow{flow})
			require.Nil(t, registry)
			require.ErrorContains(t, err, testCase.error)
		})
	}
}

func TestRegistryRejectsInvalidActionPermissions(t *testing.T) {
	invalidPermissions := []string{
		"",
		"Refund.approve",
		"refund approve",
		"refund_approve",
		"refund/approve",
		"refund..approve",
		"refund--approve",
		".refund",
		"refund-",
	}
	for _, permission := range invalidPermissions {
		t.Run(permission, func(t *testing.T) {
			status := DefineAttribute[string]("status")
			flow := &registrationFlow{
				flowType: "invalid-permission",
				schema:   PersistenceSchema{Attributes: []AttributeDef{status}},
			}
			flow.rpcs = []RPCDef{DefineRPC(flow.Update, &RPCOptions{Action: DefineAction(
				"Approve",
				WhenAttributeMatches(status, AttributeMatchEqual("A")),
				ActionRequiresPermission(permission),
			)})}
			registry, err := NewRegistry([]Flow{flow})
			require.Nil(t, registry)
			require.ErrorContains(t, err, "Action permission")
		})
	}
}

func TestRegistryRejectsWorkQueuePermissionsAttributeConflicts(t *testing.T) {
	tests := []struct {
		name      string
		attribute AttributeDef
	}{
		{
			name:      "Attribute name",
			attribute: DefineAttribute[string](WorkQueuePermissionsIndexKey),
		},
		{
			name: "custom IndexKey",
			attribute: DefineAttribute[string]("status", Indexed(AttributeIndex{
				Type:     IndexKeyword,
				IndexKey: WorkQueuePermissionsIndexKey,
			})),
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			registry, err := NewRegistry([]Flow{&registrationFlow{
				flowType: "reserved-action-index",
				schema: PersistenceSchema{
					Attributes: []AttributeDef{testCase.attribute},
				},
			}})
			require.Nil(t, registry)
			require.ErrorContains(t, err, "is reserved by Dex")
		})
	}
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
			name: "slash attribute",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				schema: PersistenceSchema{
					Attributes: []AttributeDef{DefineAttribute[string]("orders/by-id")},
				},
			}},
			error: "attribute name must not contain /",
		},
		{
			name: "slash attribute map",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				schema: PersistenceSchema{
					Attributes: []AttributeDef{DefineAttributeMap[string]("orders/by-id")},
				},
			}},
			error: "attribute name must not contain /",
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
			name: "slash channel",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				schema: PersistenceSchema{
					Channels: []ChannelDef{DefineChannel[string]("orders/by-id")},
				},
			}},
			error: "channel name must not contain /",
		},
		{
			name: "slash channel map",
			flows: []Flow{&registrationFlow{
				flowType: "flow",
				schema: PersistenceSchema{
					Channels: []ChannelDef{DefineChannelMap[string]("orders/by-id")},
				},
			}},
			error: "channel name must not contain /",
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
			name: "slash map instance",
			options: &StepOptions{
				ExecuteLockAttributes: []AttributeLock{
					LockAttributeMap(items, "tenant/order"),
				},
			},
			error: "map instance must not contain '/'",
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
	require.ErrorContains(t, err, `RPC "Update"`)
	require.ErrorContains(t, err, "direct bound method")
	require.ErrorContains(t, err, "mixed-flow")

	name, err := rpcMethodName((&mixedReceiverRegistrationFlow{}).Update)
	require.NoError(t, err)
	require.Equal(t, "Update", name)
}

func TestRPCDefinitionInvocationAndIdentity(t *testing.T) {
	pointerFlow := &registrationFlow{flowType: "pointer-flow"}
	pointerFlow.rpcs = []RPCDef{DefineRPC(pointerFlow.Update, nil)}
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
