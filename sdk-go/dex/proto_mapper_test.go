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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type mapperStep struct {
	StepDefaultsNoWaitFor[int]
	options *StepOptions
	name    string
}

type mapperNoneStep struct {
	StepDefaultsNoWaitFor[None]
	name string
}

func (step mapperStep) GetStepType() string {
	return step.name
}

func (step mapperStep) GetStepOptions() *StepOptions {
	return step.options
}

func (mapperStep) Execute(Context, int) (*StepDecision, error) {
	return DeadEnd(), nil
}

func (step mapperNoneStep) GetStepType() string {
	return step.name
}

func (mapperNoneStep) Execute(Context, None) (*StepDecision, error) {
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
	timer := Timer(time.Minute, WithConditionID("timer"))
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
	require.Equal(t, "timer", mapped.TimerConditions[0].ConditionId)
	require.Equal(t, "command", mapped.ChannelConditions[0].ConditionId)
	require.Equal(
		t,
		[]string{"timer", "command"},
		mapped.ConditionCombinations[0].ConditionIds,
	)
	require.Equal(
		t,
		[]string{"timer"},
		mapped.ConditionCombinations[1].ConditionIds,
	)
}

func TestWaitMappingRejectsInvalidConditions(t *testing.T) {
	channel := DefineChannel[string]("commands")
	channelMap := DefineChannelMap[string]("commands-by-order")
	_, err := mapWait(nil)
	require.ErrorContains(t, err, "must not be nil")

	_, err = mapWait(AllOf())
	require.ErrorContains(t, err, "at least one")

	_, err = mapWait(AllOf(
		channel.ForOne(WithConditionID("same")),
		channel.ForOne(WithConditionID("same")),
	))
	require.ErrorContains(t, err, "duplicate condition ID")

	mapped, err := mapWait(AllOf(Timer(
		time.Second,
		WithConditionID("__dex_internal_condition_1"),
	)))
	require.NoError(t, err)
	require.Equal(t, "__dex_internal_condition_1", mapped.TimerConditions[0].ConditionId)

	_, err = mapWait(AnyComboOf(Combo(Timer(time.Second))))
	require.ErrorContains(t, err, "every condition")

	mapped, err = mapWait(AllOf(Timer(time.Second), channel.ForOne()))
	require.NoError(t, err)
	require.Empty(t, mapped.TimerConditions[0].ConditionId)
	require.Empty(t, mapped.ChannelConditions[0].ConditionId)

	_, err = mapWait(AllOf(channel.ForOne(WithConditionID(""))))
	require.ErrorContains(t, err, "must not be empty")

	_, err = mapWait(AllOf(channel.AtLeastAtMost(2, 1)))
	require.ErrorContains(t, err, "below at_least")

	_, err = mapWait(AllOf(channelMap.ForOne("tenant/order")))
	require.ErrorContains(t, err, "map instance must not contain '/'")
}

func TestStepDecisionAndOptionsMapping(t *testing.T) {
	_, err := mapStepDecision(nil)
	require.ErrorContains(t, err, "must not be nil")

	status := DefineAttribute[string]("status")
	defaults := &StepOptions{
		ExecuteMethodTimeout: 5 * time.Second,
		ExecuteDurability:    StepDurabilitySync,
	}
	target := mapperStep{name: "target", options: defaults}
	decision := GoTo(
		target,
		42,
		WithStepOptions(&StepOptions{
			ExecuteMethodTimeout:  time.Second + time.Nanosecond,
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

	_, err = mapStepDecision(GoToMany())
	require.ErrorContains(t, err, "at least one")

	items := DefineAttributeMap[string]("items")
	commands := DefineChannel[string]("commands")
	commandsByTenant := DefineChannelMap[string]("commands-by-tenant")
	stateOptions, err := mapStepOptions(&StepOptions{
		WaitForLoadAttributeMaps:         []AttributeDef{items},
		WaitForLoadAttributeMapInstances: []AttributeMapLoad{items.Load("tenant-a")},
		WaitForLoadChannels:              []ChannelDef{commands},
		WaitForLoadChannelMaps:           []ChannelDef{commandsByTenant},
		WaitForLoadChannelMapInstances:   []ChannelMapLoad{commandsByTenant.LoadMessages("tenant-a")},
		ExecuteLoadAttributeMaps:         []AttributeDef{items},
		ExecuteLoadAttributeMapInstances: []AttributeMapLoad{items.Load("tenant-b")},
		ExecuteLoadChannels:              []ChannelDef{commands},
		ExecuteLoadChannelMaps:           []ChannelDef{commandsByTenant},
		ExecuteLoadChannelMapInstances:   []ChannelMapLoad{commandsByTenant.LoadMessages("tenant-b")},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"items/", "items/tenant-a"}, stateOptions.WaitForLoadAttributeMapInstances)
	require.Equal(t, []string{"commands"}, stateOptions.WaitForLoadChannelNames)
	require.Equal(t, []string{"commands-by-tenant/", "commands-by-tenant/tenant-a"}, stateOptions.WaitForLoadChannelMapInstances)
	require.Equal(t, []string{"items/", "items/tenant-b"}, stateOptions.ExecuteLoadAttributeMapInstances)
	require.Equal(t, []string{"commands"}, stateOptions.ExecuteLoadChannelNames)
	require.Equal(t, []string{"commands-by-tenant/", "commands-by-tenant/tenant-b"}, stateOptions.ExecuteLoadChannelMapInstances)
}

func TestFlowTimeoutHandlerOptionsMapping(t *testing.T) {
	items := DefineAttributeMap[string]("items")
	commands := DefineChannel[string]("commands")
	commandsByTenant := DefineChannelMap[string]("commands-by-tenant")
	recovery := mapperNoneStep{name: "recovery"}
	mapped, err := mapFlowTimeoutHandlerOptions(&FlowTimeoutHandlerOptions{
		MethodTimeout:     5 * time.Second,
		HeartbeatTimeout:  10 * time.Second,
		Retry:             &RetryPolicy{MaximumAttempts: 3},
		Failure:           ProceedToOnFlowTimeoutHandlerFailure(recovery, nil),
		Durability:        StepDurabilityAsync,
		LoadAttributeMaps: []AttributeDef{items},
		LoadAttributeMapInstances: []AttributeMapLoad{
			items.Load("tenant-a"),
		},
		LoadChannels:    []ChannelDef{commands},
		LoadChannelMaps: []ChannelDef{commandsByTenant},
		LoadChannelMapInstances: []ChannelMapLoad{
			commandsByTenant.LoadMessages("tenant-a"),
		},
	})
	require.NoError(t, err)
	require.Equal(t, int32(5), mapped.MethodTimeoutSeconds)
	require.Equal(t, int32(10), mapped.HeartbeatTimeoutSeconds)
	require.Equal(t, int32(3), mapped.RetryPolicy.MaximumAttempts)
	require.Equal(t, "recovery", mapped.FailureProceedStepType)
	require.Equal(t, dexpb.StepDurability_STEP_DURABILITY_ASYNC, mapped.DurabilityOverride)
	require.Equal(t, []string{"items/", "items/tenant-a"}, mapped.LoadAttributeMapInstances)
	require.Equal(t, []string{"commands"}, mapped.LoadChannelNames)
	require.Equal(t, []string{"commands-by-tenant/", "commands-by-tenant/tenant-a"}, mapped.LoadChannelMapInstances)
}

func TestCloseDecisionMapping(t *testing.T) {
	channel := DefineChannel[string]("commands")
	target := mapperStep{name: "fallback"}
	decision := ForceCompleteIfChannelsEmpty(
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

	_, err = mapStepDecision(ForceCompleteIfChannelsEmpty(
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
	attribute := DefineAttribute[string]("status", SyncToAttributeStore())
	initial, err := InitialAttribute(attribute, "ready")
	require.NoError(t, err)
	attributeMap := DefineAttributeMap[string]("status-by-tenant", SyncToAttributeStore())
	initialMap, err := InitialAttributeMapValue(attributeMap, "tenant-1", "ready")
	require.NoError(t, err)
	_, err = InitialAttributeMapValue(attributeMap, "tenant/order", "ready")
	require.ErrorContains(t, err, "map instance must not contain '/'")
	flowTimeout, timeoutPolicy, options, err := mapStartFlowOptions(StartFlowOptions{
		Timeout:       &timeout,
		TimeoutPolicy: TimeoutFail,
		StartDelay:    &delay,
		Attributes:    []InitialAttributeDef{initial, initialMap},
		ConfigOverride: &FlowConfig{
			ActiveStepSearchMode: &searchMode,
			StepDurability:       &durability,
			AttributeStoreNames:  []string{"reporting", "audit"},
			WorkerTarget: &WorkerTarget{
				Address:  "worker:7233",
				Headless: true,
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, int32(2), flowTimeout)
	require.Equal(t, dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_FAIL, timeoutPolicy)
	require.Equal(t, int32(2), options.FlowStartDelaySeconds)
	require.Len(t, options.Attributes, 2)
	require.True(t, options.Attributes[0].GetSyncConfig().GetEnabled())
	require.True(t, options.Attributes[1].GetSyncConfig().GetEnabled())
	require.NotNil(t, options.FlowConfigOverride.ActiveStepSearchMode)
	require.NotNil(t, options.FlowConfigOverride.StepDurability)
	require.True(t, options.FlowConfigOverride.WorkerTarget.IsHeadlessAddress)
	require.Equal(t, []string{"reporting", "audit"}, options.FlowConfigOverride.GetAttributeStoreNames().GetNames())
	require.NotNil(t, options.FlowConfigOverride.AttributeStoreNames)

	_, _, disabledOptions, err := mapStartFlowOptions(StartFlowOptions{
		ConfigOverride: &FlowConfig{AttributeStoreNames: []string{}},
	})
	require.NoError(t, err)
	require.NotNil(t, disabledOptions.FlowConfigOverride.AttributeStoreNames)
	require.Empty(t, disabledOptions.FlowConfigOverride.GetAttributeStoreNames().GetNames())

	_, _, empty, err := mapStartFlowOptions(StartFlowOptions{})
	require.NoError(t, err)
	require.Nil(t, empty.FlowConfigOverride)
}

func TestClientOptionMapping(t *testing.T) {
	attribute := DefineAttribute[string]("status")
	_, locks, err := mapRPCOptions(&RPCOptions{
		LockAttributes: []AttributeLock{LockAttribute(attribute)},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"status"}, locks)

	_, locks, err = mapRPCOptions(&RPCOptions{})
	require.NoError(t, err)
	require.Empty(t, locks)
}

func TestResultMappingRejectsUnknownEnumsAndBlobValues(t *testing.T) {
	_, err := mapFlowResult(&dexpb.FlowResult{
		FlowStatus: dexpb.FlowStatus(99),
	})
	require.ErrorContains(t, err, "unsupported flow status")

	_, err = mapFlowResult(&dexpb.FlowResult{
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

func TestFlowInfoMappingRejectsIncompleteSummaries(t *testing.T) {
	executionID := &dexpb.FlowExecutionID{FlowId: "order-123", RunId: "run-1"}
	startTime := timestamppb.New(time.Unix(100, 0))
	invalidTime := &timestamppb.Timestamp{Nanos: -1}
	testCases := []struct {
		name          string
		response      *dexpb.GetFlowSummaryResponse
		expectedError string
	}{
		{
			name:          "nil response",
			expectedError: "is missing FlowExecutionId",
		},
		{
			name: "nil execution ID",
			response: &dexpb.GetFlowSummaryResponse{
				FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING,
				StartTime:  startTime,
			},
			expectedError: "is missing FlowExecutionId",
		},
		{
			name: "missing Flow ID",
			response: &dexpb.GetFlowSummaryResponse{
				FlowExecutionId: &dexpb.FlowExecutionID{RunId: "run-1"},
				FlowType:        "OrderFlow",
				FlowStatus:      dexpb.FlowStatus_FLOW_STATUS_RUNNING,
				StartTime:       startTime,
			},
			expectedError: "is missing FlowId",
		},
		{
			name: "missing run ID",
			response: &dexpb.GetFlowSummaryResponse{
				FlowExecutionId: &dexpb.FlowExecutionID{FlowId: "order-123"},
				FlowType:        "OrderFlow",
				FlowStatus:      dexpb.FlowStatus_FLOW_STATUS_RUNNING,
				StartTime:       startTime,
			},
			expectedError: "is missing RunId",
		},
		{
			name: "missing Flow type",
			response: &dexpb.GetFlowSummaryResponse{
				FlowExecutionId: executionID,
				FlowStatus:      dexpb.FlowStatus_FLOW_STATUS_RUNNING,
				StartTime:       startTime,
			},
			expectedError: "is missing FlowType",
		},
		{
			name: "nil start time",
			response: &dexpb.GetFlowSummaryResponse{
				FlowExecutionId: executionID,
				FlowType:        "OrderFlow",
				FlowStatus:      dexpb.FlowStatus_FLOW_STATUS_RUNNING,
			},
			expectedError: "is missing StartTime",
		},
		{
			name: "unknown status",
			response: &dexpb.GetFlowSummaryResponse{
				FlowExecutionId: executionID,
				FlowType:        "OrderFlow",
				FlowStatus:      dexpb.FlowStatus(99),
				StartTime:       startTime,
			},
			expectedError: "has an invalid FlowStatus: dex: unsupported flow status 99",
		},
		{
			name: "invalid start time",
			response: &dexpb.GetFlowSummaryResponse{
				FlowExecutionId: executionID,
				FlowType:        "OrderFlow",
				FlowStatus:      dexpb.FlowStatus_FLOW_STATUS_RUNNING,
				StartTime:       invalidTime,
			},
			expectedError: "has an invalid StartTime",
		},
		{
			name: "invalid close time",
			response: &dexpb.GetFlowSummaryResponse{
				FlowExecutionId: executionID,
				FlowType:        "OrderFlow",
				FlowStatus:      dexpb.FlowStatus_FLOW_STATUS_COMPLETED,
				StartTime:       startTime,
				CloseTime:       invalidTime,
			},
			expectedError: "has an invalid CloseTime",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := mapFlowInfo("order-123", testCase.response)
			require.ErrorContains(t, err, `DescribeFlow response for Flow "order-123"`)
			require.ErrorContains(t, err, testCase.expectedError)
		})
	}
}

func TestFlowInfoMappingMapsCompleteSummary(t *testing.T) {
	startedAt := time.Unix(100, 0).UTC()
	closedAt := startedAt.Add(time.Minute)
	info, err := mapFlowInfo("order-123", &dexpb.GetFlowSummaryResponse{
		FlowExecutionId: &dexpb.FlowExecutionID{FlowId: "order-123", RunId: "run-1"},
		FlowType:        "OrderFlow",
		FlowStatus:      dexpb.FlowStatus_FLOW_STATUS_COMPLETED,
		StartTime:       timestamppb.New(startedAt),
		CloseTime:       timestamppb.New(closedAt),
	})
	require.NoError(t, err)
	require.Equal(t, FlowInfo{
		FlowID:    "order-123",
		RunID:     "run-1",
		FlowType:  "OrderFlow",
		Status:    FlowCompleted,
		StartedAt: startedAt,
		ClosedAt:  closedAt,
	}, info)
}

func TestSearchFlowsPageMappingRejectsIncompleteEntries(t *testing.T) {
	startTime := timestamppb.New(time.Unix(100, 0))
	invalidTime := &timestamppb.Timestamp{Nanos: -1}
	testCases := []struct {
		name          string
		entry         *dexpb.SearchFlowsResponseEntry
		expectedError string
	}{
		{
			name:          "nil entry",
			expectedError: "dex: SearchFlows entry 1 is nil",
		},
		{
			name: "missing Flow ID",
			entry: &dexpb.SearchFlowsResponseEntry{
				RunId:      "run-2",
				FlowType:   "OrderFlow",
				FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING,
				StartTime:  startTime,
			},
			expectedError: "dex: SearchFlows entry 1 is missing FlowId",
		},
		{
			name: "missing run ID",
			entry: &dexpb.SearchFlowsResponseEntry{
				FlowId:     "order-456",
				FlowType:   "OrderFlow",
				FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING,
				StartTime:  startTime,
			},
			expectedError: `dex: SearchFlows entry for Flow "order-456" is missing RunId`,
		},
		{
			name: "missing Flow type",
			entry: &dexpb.SearchFlowsResponseEntry{
				FlowId:     "order-456",
				RunId:      "run-2",
				FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING,
				StartTime:  startTime,
			},
			expectedError: `dex: SearchFlows entry for Flow "order-456" is missing FlowType`,
		},
		{
			name: "unknown status",
			entry: &dexpb.SearchFlowsResponseEntry{
				FlowId:     "order-456",
				RunId:      "run-2",
				FlowType:   "OrderFlow",
				FlowStatus: dexpb.FlowStatus(99),
				StartTime:  startTime,
			},
			expectedError: `dex: SearchFlows entry for Flow "order-456" has an invalid FlowStatus: dex: unsupported flow status 99`,
		},
		{
			name: "missing start time",
			entry: &dexpb.SearchFlowsResponseEntry{
				FlowId:     "order-456",
				RunId:      "run-2",
				FlowType:   "OrderFlow",
				FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING,
			},
			expectedError: `dex: SearchFlows entry for Flow "order-456" is missing StartTime`,
		},
		{
			name: "invalid start time",
			entry: &dexpb.SearchFlowsResponseEntry{
				FlowId:     "order-456",
				RunId:      "run-2",
				FlowType:   "OrderFlow",
				FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING,
				StartTime:  invalidTime,
			},
			expectedError: `dex: SearchFlows entry for Flow "order-456" has an invalid StartTime`,
		},
		{
			name: "invalid close time",
			entry: &dexpb.SearchFlowsResponseEntry{
				FlowId:     "order-456",
				RunId:      "run-2",
				FlowType:   "OrderFlow",
				FlowStatus: dexpb.FlowStatus_FLOW_STATUS_COMPLETED,
				StartTime:  startTime,
				CloseTime:  invalidTime,
			},
			expectedError: `dex: SearchFlows entry for Flow "order-456" has an invalid CloseTime`,
		},
	}
	validEntry := &dexpb.SearchFlowsResponseEntry{
		FlowId:     "order-123",
		RunId:      "run-1",
		FlowType:   "OrderFlow",
		FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING,
		StartTime:  startTime,
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := mapSearchFlowsPage(&dexpb.SearchFlowsResponse{
				FlowRuns: []*dexpb.SearchFlowsResponseEntry{validEntry, testCase.entry},
			})
			require.ErrorContains(t, err, testCase.expectedError)
		})
	}
}

func TestSearchFlowsPageMappingMapsCompleteEntries(t *testing.T) {
	startedAt := time.Unix(100, 0).UTC()
	closedAt := startedAt.Add(time.Minute)
	page, err := mapSearchFlowsPage(&dexpb.SearchFlowsResponse{
		FlowRuns: []*dexpb.SearchFlowsResponseEntry{
			{
				FlowId:     "order-123",
				RunId:      "run-1",
				FlowType:   "OrderFlow",
				FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING,
				StartTime:  timestamppb.New(startedAt),
			},
			{
				FlowId:     "order-456",
				RunId:      "run-2",
				FlowType:   "OrderFlow",
				FlowStatus: dexpb.FlowStatus_FLOW_STATUS_COMPLETED,
				StartTime:  timestamppb.New(startedAt),
				CloseTime:  timestamppb.New(closedAt),
			},
		},
		NextPageToken: "next-page",
	})
	require.NoError(t, err)
	require.Equal(t, SearchFlowsPage{
		Flows: []SearchFlowEntry{
			{
				FlowID:            "order-123",
				RunID:             "run-1",
				FlowType:          "OrderFlow",
				Status:            FlowRunning,
				StartedAt:         startedAt,
				IndexedAttributes: map[string]Value{},
			},
			{
				FlowID:            "order-456",
				RunID:             "run-2",
				FlowType:          "OrderFlow",
				Status:            FlowCompleted,
				StartedAt:         startedAt,
				ClosedAt:          closedAt,
				IndexedAttributes: map[string]Value{},
			},
		},
		NextPageToken: "next-page",
	}, page)
}

func TestFlowConfigRejectsUnknownEnums(t *testing.T) {
	unknown := StepDurability(99)
	_, err := mapFlowConfig(&FlowConfig{StepDurability: &unknown})
	require.ErrorContains(t, err, "unsupported step durability")

	_, _, err = mapStopOptions(StopOptions{Type: StopType(99)})
	require.ErrorContains(t, err, "unsupported stop type")

	_, err = mapTimeTravelOptions(TimeTravelOptions{Type: TimeTravelType(99)})
	require.ErrorContains(t, err, "unsupported time travel type")

	_, err = mapSearchFlowsOptions("", -1, "")
	require.ErrorContains(t, err, "must not be negative")

	_, err = mapFlowConfig(&FlowConfig{
		ContinueAsNewThreshold: ptr.Any(int32(1)),
	})
	require.NoError(t, err)
}
