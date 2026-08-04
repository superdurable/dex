// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import "context"

type Client struct {
	runtime any
}

func (client *Client) StartFlow(
	ctx context.Context,
	flow Flow,
	flowID string,
	input any,
	options StartFlowOptions,
) (runID string, err error) {
	return "", errPhaseNotImplemented
}

func (client *Client) PublishToChannel(
	ctx context.Context,
	flowID string,
	channel ChannelDef,
	values ...any,
) error {
	return errPhaseNotImplemented
}

func (client *Client) PublishToChannelMap(
	ctx context.Context,
	flowID string,
	channel ChannelDef,
	instance string,
	values ...any,
) error {
	return errPhaseNotImplemented
}

func (client *Client) InvokeRPC(
	ctx context.Context,
	flowID string,
	rpc any,
	input any,
	outputPtr any,
	options InvokeOptions,
) error {
	return errPhaseNotImplemented
}

func (client *Client) GetAttribute(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	valuePtr any,
) (found bool, err error) {
	return false, errPhaseNotImplemented
}

func (client *Client) GetAttributeMap(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	instance string,
	valuePtr any,
) (found bool, err error) {
	return false, errPhaseNotImplemented
}

func (client *Client) SetAttribute(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	value any,
) error {
	return errPhaseNotImplemented
}

func (client *Client) SetAttributeMap(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	instance string,
	value any,
) error {
	return errPhaseNotImplemented
}

func (client *Client) GetAttributes(
	ctx context.Context,
	flowID string,
	attributes ...AttributeDef,
) (map[string]Value, error) {
	return nil, errPhaseNotImplemented
}

func (client *Client) SetAttributes(
	ctx context.Context,
	flowID string,
	writes ...AttributeWrite,
) error {
	return errPhaseNotImplemented
}

func (client *Client) WaitForAttributeEqual(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	value any,
	options WaitOptions,
) error {
	return errPhaseNotImplemented
}

func (client *Client) WaitForAttributeMapEqual(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	instance string,
	value any,
	options WaitOptions,
) error {
	return errPhaseNotImplemented
}

func (client *Client) StopFlow(
	ctx context.Context,
	flowID string,
	options StopOptions,
) error {
	return errPhaseNotImplemented
}

func (client *Client) WaitForFlow(
	ctx context.Context,
	flowID string,
	options WaitForFlowOptions,
) (WaitForFlowResult, error) {
	return WaitForFlowResult{}, errPhaseNotImplemented
}

func (client *Client) SearchFlows(
	ctx context.Context,
	Query string,
	PageSize int32,
	NextPageToken string,
) (SearchFlowsPage, error) {
	return SearchFlowsPage{}, errPhaseNotImplemented
}

func (client *Client) ResetFlow(
	ctx context.Context,
	flowID string,
	options ResetOptions,
) (newRunID string, err error) {
	return "", errPhaseNotImplemented
}

func (client *Client) SkipTimer(
	ctx context.Context,
	flowID string,
	stepExecution StepExecutionID,
	timer TimerID,
) error {
	return errPhaseNotImplemented
}

func (client *Client) UpdateFlowConfig(
	ctx context.Context,
	flowID string,
	config FlowConfig,
) error {
	return errPhaseNotImplemented
}

func (client *Client) WaitForStepCompletion(
	ctx context.Context,
	flowID string,
	stepExecution StepExecutionID,
	options WaitOptions,
) error {
	return errPhaseNotImplemented
}

func (client *Client) TriggerContinueAsNew(
	ctx context.Context,
	flowID string,
) error {
	return errPhaseNotImplemented
}

func (client *Client) HealthCheck(
	ctx context.Context,
) (HealthInfo, error) {
	return HealthInfo{}, errPhaseNotImplemented
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
