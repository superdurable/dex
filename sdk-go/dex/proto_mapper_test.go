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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

type mapperStep struct {
	StepDefaultsNoWaitFor[int]
	options *StepOptions
	name    string
}

func (step mapperStep) GetStepType() string {
	return step.name
}

func (step mapperStep) GetStepOptions() *StepOptions {
	return step.options
}

func (mapperStep) Execute(Context, int) (StepDecision, error) {
	return DeadEnd(), nil
}

func TestDurationMappingRoundsUpAndRejectsInvalidValues(t *testing.T) {
	seconds, err := durationSeconds32(time.Second + time.Nanosecond)
	require.NoError(t, err)
	require.Equal(t, int32(2), seconds)

	seconds, err = durationSeconds32(0)
	require.NoError(t, err)
	require.Zero(t, seconds)

	_, err = durationSeconds32(-time.Nanosecond)
	require.ErrorContains(t, err, "negative")
}

func TestWaitMappingAssignsStableConditionIDs(t *testing.T) {
	channel := DefineChannel[string]("commands")
	timer := Timer(time.Minute)
	command := channel.ForOne(WithConditionID("command"))
	wait := AnyComboOf(
		Combo(timer, command),
		Combo(timer),
	)

	mapped, err := mapWait(wait)
	require.NoError(t, err)
	require.Equal(
		t,
		dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
		mapped.WaitingConditionType,
	)
	require.Equal(t, internalIDPrefix+"0", mapped.TimerConditions[0].ConditionId)
	require.Equal(t, "command", mapped.ChannelConditions[0].ConditionId)
	require.Equal(
		t,
		[]string{internalIDPrefix + "0", "command"},
		mapped.ConditionCombinations[0].ConditionIds,
	)
	require.Equal(
		t,
		[]string{internalIDPrefix + "0"},
		mapped.ConditionCombinations[1].ConditionIds,
	)
}

func TestWaitMappingRejectsInvalidConditions(t *testing.T) {
	channel := DefineChannel[string]("commands")
	_, err := mapWait(AllOf())
	require.ErrorContains(t, err, "at least one")

	_, err = mapWait(AllOf(
		channel.ForOne(WithConditionID("same")),
		channel.ForOne(WithConditionID("same")),
	))
	require.ErrorContains(t, err, "duplicate condition ID")

	_, err = mapWait(AllOf(Timer(
		time.Second,
		WithConditionID(internalIDPrefix+"1"),
	)))
	require.ErrorContains(t, err, "reserved prefix")

	_, err = mapWait(AllOf(channel.ForOne(WithConditionID(""))))
	require.ErrorContains(t, err, "must not be empty")

	_, err = mapWait(AllOf(channel.AtLeastAtMost(2, 1)))
	require.ErrorContains(t, err, "below at_least")
}

func TestStepDecisionAndOptionsMapping(t *testing.T) {
	status := DefineAttribute[string]("status")
	defaults := &StepOptions{
		ExecuteTimeout:    5 * time.Second,
		ExecuteDurability: StepDurabilitySync,
	}
	target := mapperStep{name: "target", options: defaults}
	decision := GoTo(
		target,
		42,
		WithStepOptions(&StepOptions{
			ExecuteTimeout:        time.Second + time.Nanosecond,
			ExecuteLockAttributes: []AttributeLock{LockAttribute(status)},
		}),
	)

	mapped, err := mapStepDecision(decision)
	require.NoError(t, err)
	require.Len(t, mapped.NextSteps, 1)
	require.Equal(t, "target", mapped.NextSteps[0].StepType)
	require.Equal(t, int64(42), mapped.NextSteps[0].StepInput.GetIntValue())
	require.Equal(t, int32(2), mapped.NextSteps[0].StepOptions.ExecuteTimeoutSeconds)
	require.Equal(
		t,
		dexpb.StepDurability_STEP_DURABILITY_SYNC,
		mapped.NextSteps[0].StepOptions.ExecuteDurabilityOverride,
	)
	require.Equal(
		t,
		[]string{"status"},
		mapped.NextSteps[0].StepOptions.ExecuteLockAttributeKeys,
	)

	_, err = mapStepDecision(GoToMulti())
	require.ErrorContains(t, err, "at least one")
}

func TestCloseDecisionMapping(t *testing.T) {
	channel := DefineChannel[string]("commands")
	target := mapperStep{name: "fallback"}
	decision := ForceCompleteOnChannelsEmpty(
		"done",
		[]ChannelDef{channel},
		MovementOf(target, 1),
	)

	mapped, err := mapStepDecision(decision)
	require.NoError(t, err)
	require.Equal(
		t,
		dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY,
		mapped.CloseDecision.CloseDecisionType,
	)
	require.Equal(t, []string{"commands"}, mapped.CloseDecision.ConditionalChannelNames)
	require.Equal(t, "done", mapped.CloseDecision.CloseInput.GetStringValue())

	_, err = mapStepDecision(ForceCompleteOnChannelsEmpty(
		nil,
		[]ChannelDef{channel, channel},
		MovementOf(target, 1),
	))
	require.ErrorContains(t, err, "duplicate")
}

func TestStartAndFlowConfigMappingPreservesPresence(t *testing.T) {
	timeout := time.Second + time.Nanosecond
	delay := 2 * time.Second
	searchMode := SearchAllActiveSteps
	durability := StepDurabilityAsync
	attribute := DefineAttribute[string]("status")
	initial, err := InitialAttribute(attribute, "ready")
	require.NoError(t, err)

	flowTimeout, options, err := mapStartFlowOptions(StartFlowOptions{
		Timeout:    &timeout,
		StartDelay: &delay,
		Attributes: []InitialAttributeDef{initial},
		ConfigOverride: &FlowConfig{
			ActiveStepSearchMode: &searchMode,
			StepDurability:       &durability,
			WorkerTarget: &WorkerTarget{
				Address:  "worker:7233",
				Headless: true,
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, int32(2), flowTimeout)
	require.Equal(t, int32(2), options.FlowStartDelaySeconds)
	require.Len(t, options.Attributes, 1)
	require.NotNil(t, options.FlowConfigOverride.ActiveStepSearchMode)
	require.NotNil(t, options.FlowConfigOverride.StepDurability)
	require.True(t, options.FlowConfigOverride.WorkerTarget.IsHeadlessAddress)

	_, empty, err := mapStartFlowOptions(StartFlowOptions{})
	require.NoError(t, err)
	require.Nil(t, empty.FlowConfigOverride)
}

func TestClientOptionMapping(t *testing.T) {
	timeout, err := mapWaitOptions(WaitOptions{
		Timeout: time.Second,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), timeout)

	attribute := DefineAttribute[string]("status")
	_, locks, err := mapInvokeOptions(InvokeOptions{
		LockAttributes: []AttributeLock{LockAttribute(attribute)},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"status"}, locks)

	_, locks, err = mapInvokeOptions(InvokeOptions{})
	require.NoError(t, err)
	require.Empty(t, locks)
}

func TestResultMappingRejectsUnknownEnumsAndBlobValues(t *testing.T) {
	_, err := mapWaitForFlowResult(&dexpb.WaitForFlowResponse{
		FlowStatus: dexpb.FlowStatus(99),
	})
	require.ErrorContains(t, err, "unsupported flow status")

	_, err = mapWaitForFlowResult(&dexpb.WaitForFlowResponse{
		FlowStatus: dexpb.FlowStatus_FLOW_STATUS_COMPLETED,
		Results: []*dexpb.StepCompletionOutput{{
			CompletedStepOutput: &dexpb.Value{
				Kind: &dexpb.Value_InternalBlobIdForStringValue{
					InternalBlobIdForStringValue: "blob",
				},
			},
		}},
	})
	require.ErrorContains(t, err, "not hydrated")
}

func TestFlowConfigRejectsUnknownEnums(t *testing.T) {
	unknown := StepDurability(99)
	_, err := mapFlowConfig(&FlowConfig{StepDurability: &unknown})
	require.ErrorContains(t, err, "unsupported step durability")

	_, _, err = mapStopOptions(StopOptions{Type: StopType(99)})
	require.ErrorContains(t, err, "unsupported stop type")

	_, err = mapResetOptions(ResetOptions{Type: ResetType(99)})
	require.ErrorContains(t, err, "unsupported reset type")

	_, err = mapSearchFlowsOptions("", -1, "")
	require.ErrorContains(t, err, "must not be negative")

	_, err = mapFlowConfig(&FlowConfig{
		ContinueAsNewThreshold: ptr.Any(int32(1)),
	})
	require.NoError(t, err)
}
