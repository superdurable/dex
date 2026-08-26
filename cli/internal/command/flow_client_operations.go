// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package command

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/protobuf/encoding/protojson"
)

type flowConfigInput struct {
	ActiveStepSearchMode         *string            `json:"activeStepSearchMode"`
	ContinueAsNewThreshold       *int32             `json:"continueAsNewThreshold"`
	ContinueAsNewPageSizeInBytes *int32             `json:"continueAsNewPageSizeInBytes"`
	StepDurability               *string            `json:"stepDurability"`
	WorkerTarget                 *workerTargetInput `json:"workerTarget"`
	AttributeStoreName           *string            `json:"attributeStoreName"`
}

type workerTargetInput struct {
	Address  string `json:"address"`
	Headless bool   `json:"headless"`
}

type flowRetryPolicyInput struct {
	InitialInterval    string  `json:"initialInterval"`
	BackoffCoefficient float64 `json:"backoffCoefficient"`
	MaximumInterval    string  `json:"maximumInterval"`
	MaximumAttempts    int32   `json:"maximumAttempts"`
}

type initialAttributeInput struct {
	Key   string               `json:"key"`
	Value json.RawMessage      `json:"value"`
	Index *attributeIndexInput `json:"index"`
	Sync  bool                 `json:"sync"`
}

type attributeIndexInput struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

func executeStart(c *flowCommand, ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow start", c.stderr)
	flowType := flags.String("flow-type", "", "registered Flow type")
	startStepType := flags.String("start-step-type", "", "starting Step type")
	inputSource := flags.String("input", "null", "natural JSON, @file, or - for stdin")
	attributesSource := flags.String("attributes", "", "initial Attribute JSON, @file, or - for stdin")
	configSource := flags.String("config", "", "Flow configuration JSON, @file, or - for stdin")
	retryPolicySource := flags.String("retry-policy", "", "Flow retry policy JSON, @file, or - for stdin")
	stepOptionsSource := flags.String("step-options", "", "StepOptions protobuf JSON, @file, or - for stdin")
	flowTimeout := flags.String("flow-timeout", "", "Flow timeout duration")
	timeoutPolicy := flags.String("flow-timeout-policy", "", "fail, cancel, or handler")
	idReusePolicy := flags.String("id-reuse-policy", "", "previous-failed, not-running, disallow, or terminate-running")
	startDelay := flags.String("start-delay", "", "delay before the starting Step")
	ignoreAlreadyStarted := flags.Bool("ignore-already-started", false, "return the existing run when the request ID matches")
	requestID := flags.String("request-id", "", "idempotency request ID")
	yes := flags.Bool("yes", false, "confirm the operation")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow start FLOW_ID --flow-type TYPE [flags] --yes"); done || err != nil {
		return err
	}
	flowID, err := oneFlowID(flags, "flow start")
	if err != nil {
		return err
	}
	if strings.TrimSpace(*flowType) == "" {
		return newUsageError("flow start", fmt.Errorf("flow-type is required"))
	}
	if err := atMostOneStdinSource(*inputSource, *attributesSource, *configSource, *retryPolicySource, *stepOptionsSource); err != nil {
		return newUsageError("flow start", err)
	}
	request, err := startFlowRequest(c.stdin, flowID, *flowType, *startStepType, *inputSource, *attributesSource, *configSource, *retryPolicySource, *stepOptionsSource, *flowTimeout, *timeoutPolicy, *idReusePolicy, *startDelay, *ignoreAlreadyStarted, *requestID)
	if err != nil {
		return newUsageError("flow start", err)
	}
	if !*yes {
		return newConfirmationError("flow start")
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		response, callErr := client.service.StartFlow(callCtx, request)
		if callErr != nil {
			return newOperationError("flow start", callErr)
		}
		return writeOutput(c.stdout, options.output, map[string]any{"flowId": flowID, "runId": response.GetRunId()})
	})
}

func executeWait(c *flowCommand, ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow wait", c.stderr)
	needsResults := flags.Bool("needs-results", false, "include completed Step outputs")
	waitTime := flags.String("wait-time", "0s", "server long-poll duration")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow wait FLOW_ID [--needs-results] [--wait-time DURATION]"); done || err != nil {
		return err
	}
	flowID, err := oneFlowID(flags, "flow wait")
	if err != nil {
		return err
	}
	waitSeconds, err := parseDurationSeconds(*waitTime, "wait-time")
	if err != nil {
		return newUsageError("flow wait", err)
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		response, callErr := client.service.WaitForFlow(callCtx, &dexpb.WaitForFlowRequest{FlowId: flowID, NeedsResults: *needsResults, WaitTimeSeconds: waitSeconds})
		if callErr != nil {
			return newOperationError("flow wait", callErr)
		}
		mapped, warnings, mapErr := naturalMessage(callCtx, client.service, response, options.noHydrate)
		if mapErr != nil {
			return newOperationError("flow wait", mapErr)
		}
		mapped["flowId"] = flowID
		if len(warnings) > 0 {
			mapped["warnings"] = warnings
		}
		return writeOutput(c.stdout, options.output, mapped)
	})
}

func executeSkipTimer(c *flowCommand, ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow skip-timer", c.stderr)
	stepType := flags.String("step-type", "", "Step type")
	execution := flags.Int("execution", 1, "Step execution number")
	conditionID := flags.String("condition-id", "", "Timer condition ID")
	conditionIndex := flags.String("condition-index", "", "zero-based Timer condition index")
	yes := flags.Bool("yes", false, "confirm the operation")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow skip-timer FLOW_ID --step-type TYPE [--execution N] (--condition-id ID|--condition-index N) --yes"); done || err != nil {
		return err
	}
	flowID, err := oneFlowID(flags, "flow skip-timer")
	if err != nil {
		return err
	}
	if strings.TrimSpace(*stepType) == "" || *execution <= 0 {
		return newUsageError("flow skip-timer", fmt.Errorf("step-type is required and execution must be positive"))
	}
	request := &dexpb.SkipTimerRequest{FlowId: flowID, StepExecutionId: *stepType + "-" + strconv.Itoa(*execution)}
	if (*conditionID == "") == (*conditionIndex == "") {
		return newUsageError("flow skip-timer", fmt.Errorf("exactly one of condition-id or condition-index is required"))
	}
	if *conditionID != "" {
		request.TimerConditionId = *conditionID
	} else {
		index, parseErr := parseInt32(*conditionIndex, "condition-index")
		if parseErr != nil || index < 0 {
			if parseErr != nil {
				return newUsageError("flow skip-timer", parseErr)
			}
			return newUsageError("flow skip-timer", fmt.Errorf("condition-index must not be negative"))
		}
		request.TimerConditionIndex = &index
	}
	if !*yes {
		return newConfirmationError("flow skip-timer")
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		if _, callErr := client.service.SkipTimer(callCtx, request); callErr != nil {
			return newOperationError("flow skip-timer", callErr)
		}
		return writeOutput(c.stdout, options.output, map[string]any{"flowId": flowID, "stepExecutionId": request.GetStepExecutionId(), "skipped": true})
	})
}

func executeWaitStep(c *flowCommand, ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow wait-step", c.stderr)
	stepType := flags.String("step-type", "", "Step type")
	execution := flags.Int("execution", 1, "Step execution number")
	waitTime := flags.String("wait-time", "0s", "server long-poll duration")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow wait-step FLOW_ID --step-type TYPE [--execution N] [--wait-time DURATION]"); done || err != nil {
		return err
	}
	flowID, err := oneFlowID(flags, "flow wait-step")
	if err != nil {
		return err
	}
	if strings.TrimSpace(*stepType) == "" || *execution <= 0 {
		return newUsageError("flow wait-step", fmt.Errorf("step-type is required and execution must be positive"))
	}
	waitSeconds, err := parseDurationSeconds(*waitTime, "wait-time")
	if err != nil {
		return newUsageError("flow wait-step", err)
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		_, callErr := client.service.WaitForStepCompletion(callCtx, &dexpb.WaitForStepCompletionRequest{FlowId: flowID, StepType: *stepType, StepExecutionNumber: strconv.Itoa(*execution), WaitTimeSeconds: waitSeconds, RequestId: uuid.NewString()})
		if callErr != nil {
			return newOperationError("flow wait-step", callErr)
		}
		return writeOutput(c.stdout, options.output, map[string]any{"flowId": flowID, "stepType": *stepType, "execution": *execution, "completed": true})
	})
}

func executeUpdateConfig(c *flowCommand, ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow update-config", c.stderr)
	configSource := flags.String("config", "", "Flow configuration JSON, @file, or - for stdin")
	yes := flags.Bool("yes", false, "confirm the operation")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow update-config FLOW_ID --config JSON|@FILE|- --yes"); done || err != nil {
		return err
	}
	flowID, err := oneFlowID(flags, "flow update-config")
	if err != nil {
		return err
	}
	if *configSource == "" {
		return newUsageError("flow update-config", fmt.Errorf("config is required"))
	}
	config, err := parseFlowConfig(c.stdin, *configSource)
	if err != nil {
		return newUsageError("flow update-config", err)
	}
	if !*yes {
		return newConfirmationError("flow update-config")
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		if _, callErr := client.service.UpdateFlowConfig(callCtx, &dexpb.UpdateFlowConfigRequest{FlowId: flowID, FlowConfig: config}); callErr != nil {
			return newOperationError("flow update-config", callErr)
		}
		return writeOutput(c.stdout, options.output, map[string]any{"flowId": flowID, "updated": true})
	})
}

func executeTriggerContinueAsNew(c *flowCommand, ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow trigger-continue-as-new", c.stderr)
	yes := flags.Bool("yes", false, "confirm the operation")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow trigger-continue-as-new FLOW_ID --yes"); done || err != nil {
		return err
	}
	flowID, err := oneFlowID(flags, "flow trigger-continue-as-new")
	if err != nil {
		return err
	}
	if !*yes {
		return newConfirmationError("flow trigger-continue-as-new")
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		if _, callErr := client.service.TriggerContinueAsNew(callCtx, &dexpb.TriggerContinueAsNewRequest{FlowId: flowID}); callErr != nil {
			return newOperationError("flow trigger-continue-as-new", callErr)
		}
		return writeOutput(c.stdout, options.output, map[string]any{"flowId": flowID, "requested": true})
	})
}

func startFlowRequest(reader io.Reader, flowID string, flowType string, startStepType string, inputSource string, attributesSource string, configSource string, retryPolicySource string, stepOptionsSource string, flowTimeout string, timeoutPolicy string, idReusePolicy string, startDelay string, ignoreAlreadyStarted bool, requestID string) (*dexpb.StartFlowRequest, error) {
	input, err := parseNaturalValue(reader, inputSource)
	if err != nil {
		return nil, fmt.Errorf("input: %w", err)
	}
	attributes, err := parseInitialAttributes(reader, attributesSource)
	if err != nil {
		return nil, fmt.Errorf("attributes: %w", err)
	}
	config, err := parseOptionalFlowConfig(reader, configSource)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	retryPolicy, err := parseOptionalRetryPolicy(reader, retryPolicySource)
	if err != nil {
		return nil, fmt.Errorf("retry-policy: %w", err)
	}
	stepOptions, err := parseOptionalStepOptions(reader, stepOptionsSource)
	if err != nil {
		return nil, fmt.Errorf("step-options: %w", err)
	}
	timeoutSeconds, err := parseOptionalDurationSeconds(flowTimeout, "flow-timeout")
	if err != nil {
		return nil, err
	}
	policy, err := parseFlowTimeoutPolicy(timeoutPolicy)
	if err != nil {
		return nil, err
	}
	if policy != dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_UNSPECIFIED && timeoutSeconds == 0 {
		return nil, fmt.Errorf("flow-timeout-policy requires a positive flow-timeout")
	}
	reusePolicy, err := parseIDReusePolicy(idReusePolicy)
	if err != nil {
		return nil, err
	}
	startDelaySeconds, err := parseOptionalDurationSeconds(startDelay, "start-delay")
	if err != nil {
		return nil, err
	}
	if requestID == "" {
		requestID = uuid.NewString()
	}
	startOptions := &dexpb.FlowStartOptions{IdReusePolicy: reusePolicy, FlowStartDelaySeconds: startDelaySeconds, RetryPolicy: retryPolicy, Attributes: attributes, FlowConfigOverride: config}
	if ignoreAlreadyStarted {
		startOptions.FlowAlreadyStartedOptions = &dexpb.FlowAlreadyStartedOptions{IgnoreAlreadyStartedError: true}
	}
	return &dexpb.StartFlowRequest{FlowId: flowID, FlowType: flowType, FlowTimeoutSeconds: timeoutSeconds, FlowTimeoutPolicy: policy, StartStepType: startStepType, StepInput: input, StepOptions: stepOptions, RequestId: requestID, FlowStartOptions: startOptions}, nil
}

func atMostOneStdinSource(sources ...string) error {
	stdinSources := 0
	for _, source := range sources {
		if source == "-" {
			stdinSources++
		}
	}
	if stdinSources > 1 {
		return fmt.Errorf("only one JSON option may read from stdin")
	}
	return nil
}

func parseNaturalValue(reader io.Reader, source string) (*dexpb.Value, error) {
	data, err := readFlowJSON(reader, source)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		return nil, fmt.Errorf("input must contain exactly one JSON value")
	}
	return mapNaturalValue(value)
}

func mapNaturalValue(value any) (*dexpb.Value, error) {
	switch typed := value.(type) {
	case nil:
		return jsonObjectValue(nil)
	case string:
		return &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: typed}}, nil
	case bool:
		return &dexpb.Value{Kind: &dexpb.Value_BoolValue{BoolValue: typed}}, nil
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: integer}}, nil
		}
		floating, err := typed.Float64()
		if err != nil || math.IsInf(floating, 0) || math.IsNaN(floating) {
			return nil, fmt.Errorf("number %q is outside Dex's numeric range", typed)
		}
		return &dexpb.Value{Kind: &dexpb.Value_DoubleValue{DoubleValue: floating}}, nil
	default:
		return jsonObjectValue(typed)
	}
}

func jsonObjectValue(value any) (*dexpb.Value, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode JSON value: %w", err)
	}
	return &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "json", Payload: payload}}}, nil
}

func parseInitialAttributes(reader io.Reader, source string) ([]*dexpb.AttributeWrite, error) {
	if source == "" {
		return nil, nil
	}
	data, err := readFlowJSON(reader, source)
	if err != nil {
		return nil, err
	}
	var inputs []initialAttributeInput
	if err := json.Unmarshal(data, &inputs); err != nil {
		return nil, fmt.Errorf("must be a JSON array: %w", err)
	}
	attributes := make([]*dexpb.AttributeWrite, 0, len(inputs))
	for _, input := range inputs {
		if strings.TrimSpace(input.Key) == "" || len(input.Value) == 0 {
			return nil, fmt.Errorf("every attribute requires key and value")
		}
		value, valueErr := parseNaturalValue(bytes.NewReader(input.Value), "-")
		if valueErr != nil {
			return nil, fmt.Errorf("attribute %q: %w", input.Key, valueErr)
		}
		index, indexErr := mapAttributeIndex(input.Index)
		if indexErr != nil {
			return nil, fmt.Errorf("attribute %q: %w", input.Key, indexErr)
		}
		attribute := &dexpb.AttributeWrite{Key: input.Key, Value: value, IndexConfig: index}
		if input.Sync {
			attribute.SyncConfig = &dexpb.AttributeSyncConfig{Enabled: true}
		}
		attributes = append(attributes, attribute)
	}
	return attributes, nil
}

func mapAttributeIndex(input *attributeIndexInput) (*dexpb.IndexConfig, error) {
	if input == nil {
		return nil, nil
	}
	indexType, err := parseIndexType(input.Type)
	if err != nil {
		return nil, err
	}
	return &dexpb.IndexConfig{Enable: true, Type: indexType, IndexKey: input.Key}, nil
}

func parseIndexType(value string) (dexpb.IndexType, error) {
	switch value {
	case "keyword":
		return dexpb.IndexType_INDEX_TYPE_KEYWORD, nil
	case "text":
		return dexpb.IndexType_INDEX_TYPE_TEXT, nil
	case "keyword-array":
		return dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY, nil
	case "int":
		return dexpb.IndexType_INDEX_TYPE_INT, nil
	case "double":
		return dexpb.IndexType_INDEX_TYPE_DOUBLE, nil
	case "bool":
		return dexpb.IndexType_INDEX_TYPE_BOOL, nil
	case "datetime":
		return dexpb.IndexType_INDEX_TYPE_DATETIME, nil
	default:
		return dexpb.IndexType_INDEX_TYPE_UNSPECIFIED, fmt.Errorf("index type must be keyword, text, keyword-array, int, double, bool, or datetime")
	}
}

func parseOptionalFlowConfig(reader io.Reader, source string) (*dexpb.FlowConfig, error) {
	if source == "" {
		return nil, nil
	}
	return parseFlowConfig(reader, source)
}

func parseFlowConfig(reader io.Reader, source string) (*dexpb.FlowConfig, error) {
	data, err := readFlowJSON(reader, source)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		return nil, fmt.Errorf("config must be a JSON object")
	}
	var input flowConfigInput
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, fmt.Errorf("invalid config JSON: %w", err)
	}
	config := &dexpb.FlowConfig{ContinueAsNewThreshold: input.ContinueAsNewThreshold, ContinueAsNewPageSizeInBytes: input.ContinueAsNewPageSizeInBytes, AttributeSyncConfigName: input.AttributeStoreName}
	if input.ActiveStepSearchMode != nil {
		mode, modeErr := parseActiveStepSearchMode(*input.ActiveStepSearchMode)
		if modeErr != nil {
			return nil, modeErr
		}
		config.ActiveStepSearchMode = &mode
	}
	if input.StepDurability != nil {
		durability, durabilityErr := parseStepDurability(*input.StepDurability)
		if durabilityErr != nil {
			return nil, durabilityErr
		}
		config.StepDurability = &durability
	}
	if input.WorkerTarget != nil {
		if strings.TrimSpace(input.WorkerTarget.Address) == "" {
			return nil, fmt.Errorf("workerTarget.address is required")
		}
		config.WorkerTarget = &dexpb.WorkerTarget{Address: input.WorkerTarget.Address, IsHeadlessAddress: input.WorkerTarget.Headless}
	}
	return config, nil
}

func parseActiveStepSearchMode(value string) (dexpb.ActiveStepSearchMode, error) {
	switch value {
	case "all":
		return dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL, nil
	case "wait-for":
		return dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR, nil
	case "disabled":
		return dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_DISABLED, nil
	default:
		return dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_UNSPECIFIED, fmt.Errorf("activeStepSearchMode must be all, wait-for, or disabled")
	}
}

func parseStepDurability(value string) (dexpb.StepDurability, error) {
	switch value {
	case "sync":
		return dexpb.StepDurability_STEP_DURABILITY_SYNC, nil
	case "async":
		return dexpb.StepDurability_STEP_DURABILITY_ASYNC, nil
	default:
		return dexpb.StepDurability_STEP_DURABILITY_UNSPECIFIED, fmt.Errorf("stepDurability must be sync or async")
	}
}

func parseOptionalRetryPolicy(reader io.Reader, source string) (*dexpb.FlowRetryPolicy, error) {
	if source == "" {
		return nil, nil
	}
	data, err := readFlowJSON(reader, source)
	if err != nil {
		return nil, err
	}
	var input flowRetryPolicyInput
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, fmt.Errorf("invalid retry-policy JSON: %w", err)
	}
	initial, err := parseOptionalDurationSeconds(input.InitialInterval, "retry-policy.initialInterval")
	if err != nil {
		return nil, err
	}
	maximum, err := parseOptionalDurationSeconds(input.MaximumInterval, "retry-policy.maximumInterval")
	if err != nil {
		return nil, err
	}
	if math.IsNaN(input.BackoffCoefficient) || math.IsInf(input.BackoffCoefficient, 0) || input.BackoffCoefficient > math.MaxFloat32 || input.BackoffCoefficient < -math.MaxFloat32 {
		return nil, fmt.Errorf("retry-policy.backoffCoefficient must be a finite float32")
	}
	return &dexpb.FlowRetryPolicy{InitialIntervalSeconds: initial, BackoffCoefficient: float32(input.BackoffCoefficient), MaximumIntervalSeconds: maximum, MaximumAttempts: input.MaximumAttempts}, nil
}

func parseOptionalStepOptions(reader io.Reader, source string) (*dexpb.StepOptions, error) {
	if source == "" {
		return nil, nil
	}
	data, err := readFlowJSON(reader, source)
	if err != nil {
		return nil, err
	}
	options := &dexpb.StepOptions{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(data, options); err != nil {
		return nil, fmt.Errorf("invalid StepOptions protobuf JSON: %w", err)
	}
	return options, nil
}

func parseFlowTimeoutPolicy(value string) (dexpb.FlowTimeoutPolicy, error) {
	switch value {
	case "":
		return dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_UNSPECIFIED, nil
	case "fail":
		return dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_FAIL, nil
	case "cancel":
		return dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_CANCEL, nil
	case "handler":
		return dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER, nil
	default:
		return dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_UNSPECIFIED, fmt.Errorf("flow-timeout-policy must be fail, cancel, or handler")
	}
}

func parseIDReusePolicy(value string) (dexpb.IdReusePolicy, error) {
	switch value {
	case "":
		return dexpb.IdReusePolicy_ID_REUSE_POLICY_UNSPECIFIED, nil
	case "previous-failed":
		return dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_IF_PREVIOUS_EXISTS_ABNORMALLY, nil
	case "not-running":
		return dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_IF_NO_RUNNING, nil
	case "disallow":
		return dexpb.IdReusePolicy_ID_REUSE_POLICY_DISALLOW_REUSE, nil
	case "terminate-running":
		return dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING, nil
	default:
		return dexpb.IdReusePolicy_ID_REUSE_POLICY_UNSPECIFIED, fmt.Errorf("id-reuse-policy must be previous-failed, not-running, disallow, or terminate-running")
	}
}

func parseOptionalDurationSeconds(value string, name string) (int32, error) {
	if value == "" {
		return 0, nil
	}
	return parseDurationSeconds(value, name)
}

func parseDurationSeconds(value string, name string) (int32, error) {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", name, err)
	}
	if duration < 0 {
		return 0, fmt.Errorf("%s must not be negative", name)
	}
	seconds := duration / time.Second
	if duration%time.Second != 0 {
		seconds++
	}
	if seconds > math.MaxInt32 {
		return 0, fmt.Errorf("%s exceeds int32 seconds", name)
	}
	return int32(seconds), nil
}

func readFlowJSON(reader io.Reader, source string) ([]byte, error) {
	switch {
	case source == "-":
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		return data, nil
	case strings.HasPrefix(source, "@"):
		path := strings.TrimPrefix(source, "@")
		if path == "" {
			return nil, fmt.Errorf("JSON file path must not be empty")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read JSON file: %w", err)
		}
		return data, nil
	default:
		return []byte(source), nil
	}
}
