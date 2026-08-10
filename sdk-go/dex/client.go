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
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/superdurable/dex/sdk-go/dex/blobcache"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

var errClientClosed = errors.New("dex: Client is closed")

// ClientOptions configures the FlowService client.
type ClientOptions struct {
	// FlowServiceAddress is Dex's plaintext gRPC target. Default: "localhost:8801".
	FlowServiceAddress string
	// WorkerTarget is advertised by StartFlow unless overridden. Default: nil.
	WorkerTarget *WorkerTarget
	// Logger defaults to the shared BlobCache logger.
	Logger Logger
}

// Client calls FlowService with registered typed definitions.
type Client struct {
	registry     *Registry
	cache        *blobcache.Cache
	service      dexpb.FlowServiceClient
	hydrator     valueHydrator
	connection   *grpc.ClientConn
	workerTarget *WorkerTarget
	logger       Logger

	lifecycleMu sync.RWMutex
	closed      bool
}

// NewClient constructs a Client from shared dependencies.
func NewClient(
	registry *Registry,
	cache *blobcache.Cache,
	options ClientOptions,
) (*Client, error) {
	if registry == nil {
		panic("dex.NewClient requires Registry")
	}
	if cache == nil {
		panic("dex.NewClient requires BlobCache")
	}
	flowServiceAddress, workerTarget, err := resolveClientOptions(options)
	if err != nil {
		return nil, err
	}
	connection, err := grpc.NewClient(
		flowServiceAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("dex: create FlowService client: %w", err)
	}
	return newClient(
		registry,
		cache,
		connection,
		workerTarget,
		resolveLogger(options.Logger, cache.Logger()),
	), nil
}

func newClient(
	registry *Registry,
	cache *blobcache.Cache,
	connection *grpc.ClientConn,
	workerTarget *WorkerTarget,
	logger Logger,
) *Client {
	logger = resolveLogger(logger, cache.Logger())
	service := dexpb.NewFlowServiceClient(connection)
	return &Client{
		registry:     registry,
		cache:        cache,
		service:      service,
		hydrator:     newValueHydrator(service, cache, logger),
		connection:   connection,
		workerTarget: workerTarget,
		logger:       logger,
	}
}

func resolveClientOptions(options ClientOptions) (string, *WorkerTarget, error) {
	flowServiceAddress := strings.TrimSpace(options.FlowServiceAddress)
	if flowServiceAddress == "" {
		flowServiceAddress = defaultFlowServiceTarget
	}
	if err := validatePlaintextTarget(flowServiceAddress, false); err != nil {
		return "", nil, fmt.Errorf("dex: invalid FlowService address: %w", err)
	}
	if options.WorkerTarget == nil {
		return flowServiceAddress, nil, nil
	}
	workerTarget := *options.WorkerTarget
	workerTarget.Address = strings.TrimSpace(workerTarget.Address)
	if workerTarget.Address == "" {
		return "", nil, fmt.Errorf("dex: Client Worker target address must not be empty")
	}
	if err := validatePlaintextTarget(workerTarget.Address, workerTarget.Headless); err != nil {
		return "", nil, fmt.Errorf("dex: invalid Client Worker target: %w", err)
	}
	return flowServiceAddress, &workerTarget, nil
}

// Close closes the owned FlowService connection.
func (client *Client) Close() error {
	client.lifecycleMu.Lock()
	defer client.lifecycleMu.Unlock()
	if client.closed {
		return nil
	}
	client.closed = true
	return client.connection.Close()
}

func (client *Client) validateCall(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("dex: context must not be nil")
	}
	client.lifecycleMu.RLock()
	closed := client.closed
	client.lifecycleMu.RUnlock()
	if closed {
		return errClientClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (client *Client) validateFlowCall(ctx context.Context, flowID string) error {
	if err := client.validateCall(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(flowID) == "" {
		return fmt.Errorf("dex: flow ID must not be empty")
	}
	return nil
}

func (client *Client) hydrateValues(
	ctx context.Context,
	valuePointers []**dexpb.Value,
) error {
	if err := client.hydrator.HydrateValuesInPlace(ctx, valuePointers); err != nil {
		var failure *workerFailure
		if errors.As(err, &failure) {
			return translateRPCError(failure.cause, "LoadBlobs", "", flowTargetNone)
		}
		return translateRPCError(err, "LoadBlobs", "", flowTargetNone)
	}
	return nil
}

type AttributeWrite struct {
	Name  string
	Value any
	Index *AttributeIndex
}

type FlowStatus uint8

const (
	FlowRunning FlowStatus = iota + 1
	FlowCompleted
	FlowFailed
	FlowTimedOut
	FlowTerminated
	FlowCanceled
	FlowContinuedAsNew
)

// String returns the Flow status name.
func (status FlowStatus) String() string {
	switch status {
	case FlowRunning:
		return "running"
	case FlowCompleted:
		return "completed"
	case FlowFailed:
		return "failed"
	case FlowTimedOut:
		return "timed out"
	case FlowTerminated:
		return "terminated"
	case FlowCanceled:
		return "canceled"
	case FlowContinuedAsNew:
		return "continued as new"
	default:
		return fmt.Sprintf("unknown status %d", status)
	}
}

type FlowErrorType uint8

const (
	FlowErrorStepDecision FlowErrorType = iota + 1
	FlowErrorClientAPI
	FlowErrorWorkerMethod
	FlowErrorInvalidUserCode
	FlowErrorInternal
)

type StepCompletion struct {
	StepType        string
	StepExecutionID string
	Output          Value
}

type WaitForFlowResult struct {
	Status       FlowStatus
	Completions  []StepCompletion
	ErrorType    FlowErrorType
	ErrorMessage string
}

type SearchFlowEntry struct {
	FlowID           string
	RunID            string
	FlowType         string
	Status           FlowStatus
	StartedAt        time.Time
	ClosedAt         time.Time
	SearchAttributes map[string]Value
}

type SearchFlowsPage struct {
	Flows         []SearchFlowEntry
	NextPageToken string
}

type HealthInfo struct {
	Condition string
	Hostname  string
	Duration  int32
}

func (client *Client) StartFlow(
	ctx context.Context,
	flow Flow,
	flowID string,
	input any,
	options StartFlowOptions,
) (runID string, err error) {
	if err := client.validateFlowCall(ctx, flowID); err != nil {
		return "", err
	}
	registered, err := client.registry.resolveFlow(flow)
	if err != nil {
		return "", err
	}
	startStepType, stepInput, stepOptions, err := mapStartingStep(registered, input)
	if err != nil {
		return "", err
	}
	resolvedOptions, err := client.resolveStartFlowOptions(registered, options)
	if err != nil {
		return "", err
	}
	timeout, flowOptions, err := mapStartFlowOptions(resolvedOptions)
	if err != nil {
		return "", err
	}
	requestID, err := resolveStartRequestID(options.RequestID)
	if err != nil {
		return "", err
	}
	response, err := client.service.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId:             flowID,
		FlowType:           registered.flowType,
		FlowTimeoutSeconds: timeout,
		StartStepType:      startStepType,
		StepInput:          stepInput,
		StepOptions:        stepOptions,
		FlowStartOptions:   flowOptions,
		RequestId:          requestID,
	})
	if err != nil {
		return "", translateRPCError(err, "StartFlow", flowID, flowTargetNone)
	}
	if response == nil || response.RunId == "" {
		return "", fmt.Errorf("dex: StartFlow response has no run ID")
	}
	return response.RunId, nil
}

func mapStartingStep(
	flow *registeredFlow,
	input any,
) (string, *dexpb.Value, *dexpb.StepOptions, error) {
	if flow.startingStep == nil {
		if !nilInterface(input) {
			return "", nil, nil, fmt.Errorf(
				"dex: flow %q has no starting step and requires nil input",
				flow.flowType,
			)
		}
		return "", nil, nil, nil
	}
	if !assignableValue(input, flow.startingStep.inputType) {
		return "", nil, nil, fmt.Errorf(
			"dex: starting input %T is not assignable to step %q input %s",
			input,
			flow.startingStep.stepType,
			flow.startingStep.inputType,
		)
	}
	encoded, err := encodeValue(input)
	if err != nil {
		return "", nil, nil, err
	}
	options, err := mapRegisteredStepOptions(flow.startingStep, nil)
	if err != nil {
		return "", nil, nil, err
	}
	return flow.startingStep.stepType, encoded, options, nil
}

func (client *Client) resolveStartFlowOptions(
	flow *registeredFlow,
	options StartFlowOptions,
) (StartFlowOptions, error) {
	resolved := options
	attributes, err := validateInitialAttributes(flow, options.Attributes)
	if err != nil {
		return StartFlowOptions{}, err
	}
	resolved.Attributes = attributes
	config, err := client.resolveStartFlowConfig(options.ConfigOverride)
	if err != nil {
		return StartFlowOptions{}, err
	}
	resolved.ConfigOverride = config
	return resolved, nil
}

func validateInitialAttributes(
	flow *registeredFlow,
	definitions []InitialAttributeDef,
) ([]InitialAttributeDef, error) {
	resolved := make([]InitialAttributeDef, 0, len(definitions))
	seen := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		concrete, ok := definition.(initialAttribute)
		if !ok {
			return nil, fmt.Errorf("dex: invalid initial attribute %T", definition)
		}
		registered, found := flow.attributes[concrete.name]
		if !found {
			return nil, fmt.Errorf("dex: attribute %q is not declared", concrete.name)
		}
		if registered.isMap != concrete.isMap {
			return nil, fmt.Errorf(
				"dex: attribute %q static/map kind does not match its definition",
				concrete.name,
			)
		}
		if !reflect.DeepEqual(registered.index, concrete.index) {
			return nil, fmt.Errorf(
				"dex: attribute %q index does not match its registered definition",
				concrete.name,
			)
		}
		physical, err := physicalName(concrete.name, concrete.instance, concrete.isMap)
		if err != nil {
			return nil, err
		}
		if _, found := seen[physical]; found {
			return nil, fmt.Errorf("dex: duplicate initial attribute %q", physical)
		}
		seen[physical] = struct{}{}
		encoded, indexConfig, err := encodeAttributeValue(concrete.value, registered.index)
		if err != nil {
			return nil, err
		}
		concrete.index = registered.index
		concrete.encoded = encoded
		concrete.indexConfig = indexConfig
		resolved = append(resolved, concrete)
	}
	return resolved, nil
}

func (client *Client) resolveStartFlowConfig(config *FlowConfig) (*FlowConfig, error) {
	if config == nil && client.workerTarget == nil {
		return nil, nil
	}
	resolved := FlowConfig{}
	if config != nil {
		resolved = *config
	}
	if resolved.WorkerTarget == nil && client.workerTarget != nil {
		target := *client.workerTarget
		resolved.WorkerTarget = &target
	} else if resolved.WorkerTarget != nil {
		target := *resolved.WorkerTarget
		if err := validatePlaintextTarget(target.Address, target.Headless); err != nil {
			return nil, fmt.Errorf("dex: invalid StartFlow Worker target: %w", err)
		}
		resolved.WorkerTarget = &target
	}
	return &resolved, nil
}

func resolveStartRequestID(override *string) (string, error) {
	if override == nil {
		return newRequestID()
	}
	if *override == "" {
		return "", fmt.Errorf("dex: StartFlow request ID must not be empty")
	}
	return *override, nil
}

func (client *Client) StopFlow(
	ctx context.Context,
	flowID string,
	options StopOptions,
) error {
	if err := client.validateFlowCall(ctx, flowID); err != nil {
		return err
	}
	stopType, reason, err := mapStopOptions(options)
	if err != nil {
		return err
	}
	_, err = client.service.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowID,
		StopType: stopType,
		Reason:   reason,
	})
	return translateRPCError(err, "StopFlow", flowID, flowTargetActive)
}

func (client *Client) WaitForFlow(
	ctx context.Context,
	flowID string,
	options WaitForFlowOptions,
) (WaitForFlowResult, error) {
	if err := client.validateFlowCall(ctx, flowID); err != nil {
		return WaitForFlowResult{}, err
	}
	needsResults, timeout, err := mapWaitForFlowOptions(options)
	if err != nil {
		return WaitForFlowResult{}, err
	}
	response, err := client.service.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		NeedsResults:    needsResults,
		WaitTimeSeconds: timeout,
	})
	if err != nil {
		return WaitForFlowResult{}, translateRPCError(
			err,
			"WaitForFlow",
			flowID,
			flowTargetExisting,
		)
	}
	if err := client.hydrateValues(ctx, waitForFlowValuePointers(response)); err != nil {
		return WaitForFlowResult{}, err
	}
	result, err := mapWaitForFlowResult(response)
	if err != nil {
		return WaitForFlowResult{}, err
	}
	if result.Status == FlowCompleted {
		return result, nil
	}
	runID, err := client.flowRunID(ctx, flowID)
	if err != nil {
		return WaitForFlowResult{}, err
	}
	return WaitForFlowResult{}, &FlowUncompletedError{
		FlowID:       flowID,
		RunID:        runID,
		Status:       result.Status,
		ErrorType:    result.ErrorType,
		ErrorMessage: result.ErrorMessage,
		Completions:  result.Completions,
	}
}

func (client *Client) flowRunID(ctx context.Context, flowID string) (string, error) {
	response, err := client.service.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{
		FlowId: flowID,
	})
	if err != nil {
		return "", translateRPCError(err, "GetFlowSummary", flowID, flowTargetExisting)
	}
	if response == nil || response.FlowExecutionId == nil ||
		response.FlowExecutionId.RunId == "" {
		return "", fmt.Errorf("dex: GetFlowSummary response has no run ID")
	}
	return response.FlowExecutionId.RunId, nil
}

func waitForFlowValuePointers(response *dexpb.WaitForFlowResponse) []**dexpb.Value {
	if response == nil {
		return nil
	}
	pointers := make([]**dexpb.Value, 0, len(response.Results))
	for _, completion := range response.Results {
		if completion != nil {
			pointers = append(pointers, &completion.CompletedStepOutput)
		}
	}
	return pointers
}

func (client *Client) SearchFlows(
	ctx context.Context,
	query string,
	pageSize int32,
	nextPageToken string,
) (SearchFlowsPage, error) {
	if err := client.validateCall(ctx); err != nil {
		return SearchFlowsPage{}, err
	}
	request, err := mapSearchFlowsOptions(query, pageSize, nextPageToken)
	if err != nil {
		return SearchFlowsPage{}, err
	}
	response, err := client.service.SearchFlows(ctx, request)
	if err != nil {
		return SearchFlowsPage{}, translateRPCError(err, "SearchFlows", "", flowTargetNone)
	}
	if err := client.hydrateValues(ctx, searchFlowValuePointers(response)); err != nil {
		return SearchFlowsPage{}, err
	}
	return mapSearchFlowsPage(response)
}

func searchFlowValuePointers(response *dexpb.SearchFlowsResponse) []**dexpb.Value {
	if response == nil {
		return nil
	}
	var pointers []**dexpb.Value
	for _, flow := range response.FlowRuns {
		if flow == nil {
			continue
		}
		for _, attribute := range flow.SearchAttributes {
			if attribute != nil {
				pointers = append(pointers, &attribute.Value)
			}
		}
	}
	return pointers
}

func (client *Client) ResetFlow(
	ctx context.Context,
	flowID string,
	options ResetOptions,
) (newRunID string, err error) {
	if err := client.validateFlowCall(ctx, flowID); err != nil {
		return "", err
	}
	request, err := mapResetOptions(options)
	if err != nil {
		return "", err
	}
	request.FlowId = flowID
	response, err := client.service.ResetFlow(ctx, request)
	if err != nil {
		return "", translateRPCError(err, "ResetFlow", flowID, flowTargetExisting)
	}
	if response == nil || response.RunId == "" {
		return "", fmt.Errorf("dex: ResetFlow response has no run ID")
	}
	return response.RunId, nil
}

func (client *Client) SkipTimer(
	ctx context.Context,
	flowID string,
	stepExecution StepExecutionID,
	timer TimerID,
) error {
	if err := client.validateFlowCall(ctx, flowID); err != nil {
		return err
	}
	executionNumber, err := effectiveExecutionNumber(stepExecution)
	if err != nil {
		return err
	}
	conditionID, conditionIndex, err := resolveTimerID(timer)
	if err != nil {
		return err
	}
	_, err = client.service.SkipTimer(ctx, &dexpb.SkipTimerRequest{
		FlowId:              flowID,
		StepExecutionId:     stepExecution.StepType + "-" + strconv.FormatInt(int64(executionNumber), 10),
		TimerConditionId:    conditionID,
		TimerConditionIndex: conditionIndex,
	})
	return translateRPCError(err, "SkipTimer", flowID, flowTargetActive)
}

func effectiveExecutionNumber(stepExecution StepExecutionID) (int32, error) {
	if stepExecution.StepType == "" {
		return 0, fmt.Errorf("dex: step type must not be empty")
	}
	if stepExecution.ExecutionNumber == nil {
		return 1, nil
	}
	if *stepExecution.ExecutionNumber <= 0 {
		return 0, fmt.Errorf("dex: step execution number must be positive")
	}
	return *stepExecution.ExecutionNumber, nil
}

func resolveTimerID(timer TimerID) (string, *int32, error) {
	hasID := timer.ConditionID != ""
	hasIndex := timer.Index != nil
	if hasID == hasIndex {
		return "", nil, fmt.Errorf("dex: timer requires exactly one condition ID or index")
	}
	if hasIndex && *timer.Index < 0 {
		return "", nil, fmt.Errorf("dex: timer condition index must not be negative")
	}
	return timer.ConditionID, timer.Index, nil
}

func (client *Client) UpdateFlowConfig(
	ctx context.Context,
	flowID string,
	config FlowConfig,
) error {
	if err := client.validateFlowCall(ctx, flowID); err != nil {
		return err
	}
	mapped, err := mapFlowConfig(&config)
	if err != nil {
		return err
	}
	_, err = client.service.UpdateFlowConfig(ctx, &dexpb.UpdateFlowConfigRequest{
		FlowId:     flowID,
		FlowConfig: mapped,
	})
	return translateRPCError(err, "UpdateFlowConfig", flowID, flowTargetActive)
}

func (client *Client) WaitForStepCompletion(
	ctx context.Context,
	flowID string,
	stepExecution StepExecutionID,
	options WaitOptions,
) error {
	if err := client.validateFlowCall(ctx, flowID); err != nil {
		return err
	}
	executionNumber, err := effectiveExecutionNumber(stepExecution)
	if err != nil {
		return err
	}
	timeout, err := mapWaitOptions(options)
	if err != nil {
		return err
	}
	requestID, err := newRequestID()
	if err != nil {
		return err
	}
	_, err = client.service.WaitForStepCompletion(
		ctx,
		&dexpb.WaitForStepCompletionRequest{
			FlowId:              flowID,
			StepType:            stepExecution.StepType,
			StepExecutionNumber: strconv.FormatInt(int64(executionNumber), 10),
			WaitTimeSeconds:     timeout,
			RequestId:           requestID,
		},
	)
	return translateRPCError(err, "WaitForStepCompletion", flowID, flowTargetActive)
}

func (client *Client) TriggerContinueAsNew(
	ctx context.Context,
	flowID string,
) error {
	if err := client.validateFlowCall(ctx, flowID); err != nil {
		return err
	}
	_, err := client.service.TriggerContinueAsNew(
		ctx,
		&dexpb.TriggerContinueAsNewRequest{FlowId: flowID},
	)
	return translateRPCError(err, "TriggerContinueAsNew", flowID, flowTargetActive)
}

func (client *Client) HealthCheck(ctx context.Context) (HealthInfo, error) {
	if err := client.validateCall(ctx); err != nil {
		return HealthInfo{}, err
	}
	response, err := client.service.HealthCheck(ctx, &emptypb.Empty{})
	if err != nil {
		return HealthInfo{}, translateRPCError(err, "HealthCheck", "", flowTargetNone)
	}
	return mapHealthInfo(response)
}

func (client *Client) PublishToChannel(
	ctx context.Context,
	flowID string,
	channel ChannelDef,
	values ...any,
) error {
	return client.publishToChannel(ctx, flowID, channel, "", false, values)
}

func (client *Client) PublishToChannelMap(
	ctx context.Context,
	flowID string,
	channel ChannelDef,
	instance string,
	values ...any,
) error {
	return client.publishToChannel(ctx, flowID, channel, instance, true, values)
}

func (client *Client) publishToChannel(
	ctx context.Context,
	flowID string,
	channel ChannelDef,
	instance string,
	isMap bool,
	values []any,
) error {
	if err := client.validateFlowCall(ctx, flowID); err != nil {
		return err
	}
	registered, err := client.registry.resolveChannel(channel, isMap)
	if err != nil {
		return err
	}
	name, err := physicalName(registered.name, instance, isMap)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}
	messages := make([]*dexpb.ChannelMessage, 0, len(values))
	for _, value := range values {
		encoded, err := encodeValue(value)
		if err != nil {
			return err
		}
		messages = append(messages, &dexpb.ChannelMessage{
			ChannelName: name,
			Value:       encoded,
		})
	}
	_, err = client.service.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId:   flowID,
		Messages: messages,
	})
	return translateRPCError(err, "PublishToChannel", flowID, flowTargetActive)
}

func (client *Client) GetAttribute(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	valuePtr any,
) (found bool, err error) {
	return client.getAttribute(ctx, flowID, attribute, "", false, valuePtr)
}

func (client *Client) GetAttributeMap(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	instance string,
	valuePtr any,
) (found bool, err error) {
	return client.getAttribute(ctx, flowID, attribute, instance, true, valuePtr)
}

func (client *Client) getAttribute(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	instance string,
	isMap bool,
	valuePtr any,
) (bool, error) {
	if err := client.validateFlowCall(ctx, flowID); err != nil {
		return false, err
	}
	if _, err := decodeTarget(valuePtr); err != nil {
		return false, err
	}
	registered, err := client.registry.resolveAttribute(attribute, isMap)
	if err != nil {
		return false, err
	}
	name, err := physicalName(registered.name, instance, isMap)
	if err != nil {
		return false, err
	}
	response, err := client.service.GetAttributes(ctx, &dexpb.GetAttributesRequest{
		FlowId: flowID,
		Keys:   []string{name},
	})
	if err != nil {
		return false, translateRPCError(err, "GetAttribute", flowID, flowTargetExisting)
	}
	value, found, err := client.singleAttributeValue(ctx, response, name)
	if err != nil || !found {
		return found, err
	}
	if err := value.Decode(valuePtr); err != nil {
		return false, err
	}
	return true, nil
}

func (client *Client) singleAttributeValue(
	ctx context.Context,
	response *dexpb.GetAttributesResponse,
	expected string,
) (Value, bool, error) {
	if response == nil {
		return Value{}, false, fmt.Errorf("dex: GetAttributes response is nil")
	}
	if len(response.Attributes) == 0 {
		return Value{}, false, nil
	}
	if len(response.Attributes) != 1 || response.Attributes[0] == nil ||
		response.Attributes[0].Key != expected {
		return Value{}, false, fmt.Errorf(
			"dex: GetAttributes returned an unexpected attribute",
		)
	}
	if err := client.hydrateValues(
		ctx,
		[]**dexpb.Value{&response.Attributes[0].Value},
	); err != nil {
		return Value{}, false, err
	}
	value, err := newValue(response.Attributes[0].Value)
	return value, true, err
}

func (client *Client) SetAttribute(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	value any,
) error {
	return client.setAttribute(ctx, flowID, attribute, "", false, value)
}

func (client *Client) SetAttributeMap(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	instance string,
	value any,
) error {
	return client.setAttribute(ctx, flowID, attribute, instance, true, value)
}

func (client *Client) setAttribute(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	instance string,
	isMap bool,
	value any,
) error {
	if err := client.validateFlowCall(ctx, flowID); err != nil {
		return err
	}
	registered, err := client.registry.resolveAttribute(attribute, isMap)
	if err != nil {
		return err
	}
	name, err := physicalName(registered.name, instance, isMap)
	if err != nil {
		return err
	}
	return client.setAttributes(ctx, flowID, []AttributeWrite{{
		Name:  name,
		Value: value,
		Index: registered.index,
	}})
}

func (client *Client) GetAttributes(
	ctx context.Context,
	flowID string,
	attributes ...AttributeDef,
) (map[string]Value, error) {
	if err := client.validateFlowCall(ctx, flowID); err != nil {
		return nil, err
	}
	if len(attributes) == 0 {
		return map[string]Value{}, nil
	}
	keys := make([]string, 0, len(attributes))
	requested := make(map[string]struct{}, len(attributes))
	for _, attribute := range attributes {
		registered, err := client.registry.resolveAttribute(attribute, false)
		if err != nil {
			return nil, err
		}
		if _, found := requested[registered.name]; found {
			return nil, fmt.Errorf("dex: duplicate attribute %q", registered.name)
		}
		requested[registered.name] = struct{}{}
		keys = append(keys, registered.name)
	}
	response, err := client.service.GetAttributes(ctx, &dexpb.GetAttributesRequest{
		FlowId: flowID,
		Keys:   keys,
	})
	if err != nil {
		return nil, translateRPCError(err, "GetAttributes", flowID, flowTargetExisting)
	}
	if response == nil {
		return nil, fmt.Errorf("dex: GetAttributes response is nil")
	}
	for _, attribute := range response.Attributes {
		if attribute == nil {
			return nil, fmt.Errorf("dex: GetAttributes returned a nil attribute")
		}
		if _, found := requested[attribute.Key]; !found {
			return nil, fmt.Errorf(
				"dex: GetAttributes returned unexpected attribute %q",
				attribute.Key,
			)
		}
	}
	if err := client.hydrateValues(ctx, keyValuePointers(response.Attributes)); err != nil {
		return nil, err
	}
	return mapValues(response.Attributes)
}

func keyValuePointers(values []*dexpb.KV) []**dexpb.Value {
	pointers := make([]**dexpb.Value, 0, len(values))
	for _, value := range values {
		if value != nil {
			pointers = append(pointers, &value.Value)
		}
	}
	return pointers
}

func (client *Client) SetAttributes(
	ctx context.Context,
	flowID string,
	writes ...AttributeWrite,
) error {
	if err := client.validateFlowCall(ctx, flowID); err != nil {
		return err
	}
	return client.setAttributes(ctx, flowID, writes)
}

func (client *Client) setAttributes(
	ctx context.Context,
	flowID string,
	writes []AttributeWrite,
) error {
	if len(writes) == 0 {
		return nil
	}
	mapped := make([]*dexpb.AttributeWrite, 0, len(writes))
	seen := make(map[string]struct{}, len(writes))
	for _, write := range writes {
		if _, found := seen[write.Name]; found {
			return fmt.Errorf("dex: duplicate attribute write %q", write.Name)
		}
		seen[write.Name] = struct{}{}
		encoded, err := mapAttributeWrite(write)
		if err != nil {
			return err
		}
		mapped = append(mapped, encoded)
	}
	requestID, err := newRequestID()
	if err != nil {
		return err
	}
	_, err = client.service.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		FlowId:     flowID,
		Attributes: mapped,
		RequestId:  requestID,
	})
	return translateRPCError(err, "SetAttributes", flowID, flowTargetActive)
}

func (client *Client) WaitForAttributeEqual(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	value any,
	options WaitOptions,
) error {
	return client.waitForAttributeEqual(
		ctx,
		flowID,
		attribute,
		"",
		false,
		value,
		options,
	)
}

func (client *Client) WaitForAttributeMapEqual(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	instance string,
	value any,
	options WaitOptions,
) error {
	return client.waitForAttributeEqual(
		ctx,
		flowID,
		attribute,
		instance,
		true,
		value,
		options,
	)
}

func (client *Client) waitForAttributeEqual(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	instance string,
	isMap bool,
	value any,
	options WaitOptions,
) error {
	if err := client.validateFlowCall(ctx, flowID); err != nil {
		return err
	}
	registered, err := client.registry.resolveAttribute(attribute, isMap)
	if err != nil {
		return err
	}
	name, err := physicalName(registered.name, instance, isMap)
	if err != nil {
		return err
	}
	encoded, err := encodeValue(value)
	if err != nil {
		return err
	}
	timeout, err := mapWaitOptions(options)
	if err != nil {
		return err
	}
	requestID, err := newRequestID()
	if err != nil {
		return err
	}
	_, err = client.service.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
		FlowId: flowID,
		Condition: &dexpb.WaitForAttributeCondition{
			Kind: &dexpb.WaitForAttributeCondition_Equal{
				Equal: &dexpb.WaitForAttributeEqual{Key: name, Value: encoded},
			},
		},
		WaitTimeSeconds: timeout,
		RequestId:       requestID,
	})
	return translateRPCError(err, "WaitForAttribute", flowID, flowTargetActive)
}

func (client *Client) InvokeRPC(
	ctx context.Context,
	flowID string,
	rpc any,
	input any,
	outputPtr any,
	options InvokeOptions,
) error {
	if err := client.validateFlowCall(ctx, flowID); err != nil {
		return err
	}
	flow, registered, err := client.registry.resolveRPC(rpc)
	if err != nil {
		return err
	}
	if !assignableValue(input, registered.input) {
		return fmt.Errorf(
			"dex: RPC input %T is not assignable to %s",
			input,
			registered.input,
		)
	}
	outputTarget, err := decodeTarget(outputPtr)
	if err != nil {
		return err
	}
	if outputTarget.Type() != registered.output {
		return fmt.Errorf(
			"dex: RPC output target %s does not match %s",
			outputTarget.Type(),
			registered.output,
		)
	}
	if err := flow.validateAttributeLocks(options.LockAttributes); err != nil {
		return err
	}
	timeout, locks, err := mapInvokeOptions(options)
	if err != nil {
		return err
	}
	encoded, err := encodeValue(input)
	if err != nil {
		return err
	}
	requestID, err := newRequestID()
	if err != nil {
		return err
	}
	response, err := client.service.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		FlowId:            flowID,
		RpcName:           registered.durableName,
		Input:             encoded,
		TimeoutSeconds:    timeout,
		LockAttributeKeys: locks,
		RequestId:         requestID,
	})
	if err != nil {
		return translateRPCError(err, "InvokeRPC", flowID, flowTargetActive)
	}
	if response == nil {
		return fmt.Errorf("dex: InvokeRPC response is nil")
	}
	if err := client.hydrateValues(
		ctx,
		[]**dexpb.Value{&response.Output},
	); err != nil {
		return err
	}
	return decodeValue(response.Output, outputPtr)
}
