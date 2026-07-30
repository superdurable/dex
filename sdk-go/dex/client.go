// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
	runID string,
	channelName string,
	values ...any,
) error {
	return errPhaseNotImplemented
}

func (client *Client) PublishToChannelMap(
	ctx context.Context,
	flowID string,
	runID string,
	channelName string,
	instance string,
	values ...any,
) error {
	return errPhaseNotImplemented
}

func (client *Client) InvokeRPC(
	ctx context.Context,
	flowID string,
	runID string,
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
	runID string,
	attributeName string,
	valuePtr any,
) (found bool, err error) {
	return false, errPhaseNotImplemented
}

func (client *Client) GetAttributeMap(
	ctx context.Context,
	flowID string,
	runID string,
	attributeName string,
	instance string,
	valuePtr any,
) (found bool, err error) {
	return false, errPhaseNotImplemented
}

func (client *Client) SetAttribute(
	ctx context.Context,
	flowID string,
	runID string,
	attributeName string,
	value any,
) error {
	return errPhaseNotImplemented
}

func (client *Client) SetAttributeMap(
	ctx context.Context,
	flowID string,
	runID string,
	attributeName string,
	instance string,
	value any,
) error {
	return errPhaseNotImplemented
}

func (client *Client) DeleteAttribute(
	ctx context.Context,
	flowID string,
	runID string,
	attributeName string,
) error {
	return errPhaseNotImplemented
}

func (client *Client) DeleteAttributeMap(
	ctx context.Context,
	flowID string,
	runID string,
	attributeName string,
	instance string,
) error {
	return errPhaseNotImplemented
}

func (client *Client) WaitForAttributeEqual(
	ctx context.Context,
	flowID string,
	runID string,
	attributeName string,
	value any,
	options WaitOptions,
) error {
	return errPhaseNotImplemented
}

func (client *Client) WaitForAttributeMapEqual(
	ctx context.Context,
	flowID string,
	runID string,
	attributeName string,
	instance string,
	value any,
	options WaitOptions,
) error {
	return errPhaseNotImplemented
}

func (client *Client) GetAttributes(
	ctx context.Context,
	flowID string,
	runID string,
	attributeNames ...string,
) (map[string]Value, error) {
	return nil, errPhaseNotImplemented
}

func (client *Client) SetAttributes(
	ctx context.Context,
	flowID string,
	runID string,
	writes ...AttributeWrite,
) error {
	return errPhaseNotImplemented
}

func (client *Client) StopFlow(
	ctx context.Context,
	flowID string,
	runID string,
	options StopOptions,
) error {
	return errPhaseNotImplemented
}

func (client *Client) WaitForFlow(
	ctx context.Context,
	flowID string,
	runID string,
	options WaitForFlowOptions,
) (WaitForFlowResult, error) {
	return WaitForFlowResult{}, errPhaseNotImplemented
}

func (client *Client) SearchFlows(
	ctx context.Context,
	options SearchFlowsOptions,
) (SearchFlowsPage, error) {
	return SearchFlowsPage{}, errPhaseNotImplemented
}

func (client *Client) ResetFlow(
	ctx context.Context,
	flowID string,
	runID string,
	options ResetOptions,
) (newRunID string, err error) {
	return "", errPhaseNotImplemented
}

func (client *Client) SkipTimer(
	ctx context.Context,
	flowID string,
	runID string,
	stepExecution StepExecutionRef,
	timer TimerRef,
) error {
	return errPhaseNotImplemented
}

func (client *Client) UpdateFlowConfig(
	ctx context.Context,
	flowID string,
	runID string,
	config FlowConfig,
) error {
	return errPhaseNotImplemented
}

func (client *Client) WaitForStepCompletion(
	ctx context.Context,
	flowID string,
	stepExecution StepExecutionRef,
	options WaitOptions,
) error {
	return errPhaseNotImplemented
}

func (client *Client) TriggerContinueAsNew(
	ctx context.Context,
	flowID string,
	runID string,
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

type Value struct {
	value any
}

func (value Value) Decode(valuePtr any) error {
	return errPhaseNotImplemented
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
