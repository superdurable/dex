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

import (
	"strings"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

func fromIdlCommandResults(results *dexpb.CommandResults, encoder ObjectEncoder) (CommandResults, error) {
	if results == nil {
		return CommandResults{}, nil
	}
	var timerResults []TimerCommandResult
	var signalResults []SignalCommandResult
	var internalChannelResults []InternalChannelCommandResult
	for _, t := range results.TimerResults {
		timerResult := TimerCommandResult{
			CommandId: t.CommandId,
			Status:    t.TimerStatus,
		}
		timerResults = append(timerResults, timerResult)
	}
	for _, t := range results.SignalResults {
		signalResult := SignalCommandResult{
			CommandId:   t.CommandId,
			ChannelName: t.SignalChannelName,
			Status:      t.SignalRequestStatus,
			SignalValue: NewObject(t.SignalValue, encoder),
		}
		signalResults = append(signalResults, signalResult)
	}
	for _, t := range results.InterStateChannelResults {
		interStateChannelResult := InternalChannelCommandResult{
			CommandId:   t.CommandId,
			ChannelName: t.ChannelName,
			Status:      t.RequestStatus,
			Value:       NewObject(t.Value, encoder),
		}
		internalChannelResults = append(internalChannelResults, interStateChannelResult)
	}
	var waitUntilApiSucceeded *bool
	if results.StateWaitUntilFailed != nil {
		// The server will set stateWaitUntilFailed to true if the waitUntil API failed.
		// Hence, flag inversion is needed here to indicate that the waitUntil API
		// succeeded.
		stateWaitUntilFailed := !*results.StateWaitUntilFailed
		waitUntilApiSucceeded = &stateWaitUntilFailed
	}
	return CommandResults{
		Timers:                  timerResults,
		Signals:                 signalResults,
		InternalChannelCommands: internalChannelResults,
		WaitUntilApiSucceeded:   waitUntilApiSucceeded,
	}, nil
}

func toIdlCommandRequest(commandRequest *CommandRequest) (*dexpb.CommandRequest, error) {
	var timerCmds []dexpb.TimerCommand
	var signalCmds []dexpb.SignalCommand
	var interStateCmds []dexpb.InterStateChannelCommand
	for _, t := range commandRequest.Commands {
		commandId := t.CommandId
		if t.CommandType == CommandTypeTimer {
			timerCmd := dexpb.TimerCommand{
				CommandId:       &commandId,
				DurationSeconds: t.TimerCommand.DurationSeconds,
			}
			timerCmds = append(timerCmds, timerCmd)
		}
		if t.CommandType == CommandTypeSignalChannel {
			signalCmd := dexpb.SignalCommand{
				CommandId:         &commandId,
				SignalChannelName: t.SignalCommand.ChannelName,
			}
			signalCmds = append(signalCmds, signalCmd)
		}
		if t.CommandType == CommandTypeInternalChannel {
			interstateChannelCmd := dexpb.InterStateChannelCommand{
				CommandId:   &commandId,
				ChannelName: t.InternalChannelCommand.ChannelName,
			}
			interStateCmds = append(interStateCmds, interstateChannelCmd)
		}
	}

	idlCmdReq := &dexpb.CommandRequest{
		CommandWaitingType: commandRequest.CommandWaitingType,
	}
	if len(timerCmds) > 0 {
		idlCmdReq.TimerCommands = timerCmds
	}
	if len(signalCmds) > 0 {
		idlCmdReq.SignalCommands = signalCmds
	}
	if len(interStateCmds) > 0 {
		idlCmdReq.InterStateChannelCommands = interStateCmds
	}
	if len(commandRequest.CommandCombinations) > 0 {
		idlCmdReq.CommandCombinations = commandRequest.CommandCombinations
	}
	return idlCmdReq, nil
}

func toIdlDecision(from *StateDecision, wfType string, registry Registry, encoder ObjectEncoder) (*dexpb.StateDecision, error) {
	var mvs []dexpb.StateMovement
	for _, fromMv := range from.NextStates {
		input, err := encoder.Encode(fromMv.NextStateInput)
		if err != nil {
			return nil, err
		}
		var options *dexpb.WorkflowStateOptions
		if !strings.HasPrefix(fromMv.NextStateId, ReservedStateIdPrefix) {
			stateDef := registry.getWorkflowStateDef(wfType, fromMv.NextStateId)
			options = toIdlStateOptions(ShouldSkipWaitUntilAPI(stateDef.State), stateDef.State.GetStateOptions())
		}
		mv := dexpb.StateMovement{
			StateId:      fromMv.NextStateId,
			StateInput:   input,
			StateOptions: options,
		}
		mvs = append(mvs, mv)
	}
	return &dexpb.StateDecision{
		NextStates: mvs,
	}, nil
}

func toIdlStateOptions(skipWaitUntil bool, stateOptions *StateOptions) *dexpb.WorkflowStateOptions {
	if stateOptions == nil {
		stateOptions = &StateOptions{}
	}

	idlStOptions := &dexpb.WorkflowStateOptions{
		DataAttributesLoadingPolicy:               stateOptions.DataAttributesLoadingPolicy,
		SearchAttributesLoadingPolicy:             stateOptions.SearchAttributesLoadingPolicy,
		WaitUntilApiTimeoutSeconds:                stateOptions.WaitUntilApiTimeoutSeconds,
		WaitUntilApiRetryPolicy:                   stateOptions.WaitUntilApiRetryPolicy,
		WaitUntilApiFailurePolicy:                 stateOptions.WaitUntilApiFailurePolicy,
		WaitUntilApiDataAttributesLoadingPolicy:   stateOptions.WaitUntilApiDataAttributesLoadingPolicy,
		WaitUntilApiSearchAttributesLoadingPolicy: stateOptions.WaitUntilApiSearchAttributesLoadingPolicy,
		ExecuteApiTimeoutSeconds:                  stateOptions.ExecuteApiTimeoutSeconds,
		ExecuteApiRetryPolicy:                     stateOptions.ExecuteApiRetryPolicy,
		ExecuteApiDataAttributesLoadingPolicy:     stateOptions.ExecuteApiDataAttributesLoadingPolicy,
		ExecuteApiSearchAttributesLoadingPolicy:   stateOptions.ExecuteApiSearchAttributesLoadingPolicy,
	}

	if skipWaitUntil {
		idlStOptions.SkipWaitUntil = ptr.Any(true)
	}

	if stateOptions.ExecuteApiFailureProceedState != nil {
		idlStOptions.ExecuteApiFailurePolicy = dexpb.PROCEED_TO_CONFIGURED_STATE.Ptr()
		idlStOptions.ExecuteApiFailureProceedStateId = ptr.Any(GetFinalWorkflowStateId(stateOptions.ExecuteApiFailureProceedState))

		proceedStateOptions := stateOptions.ExecuteApiFailureProceedState.GetStateOptions()
		if proceedStateOptions != nil && proceedStateOptions.ExecuteApiFailureProceedState != nil {
			panic("nested failure handling/recovery is not supported: ExecuteApiFailureProceedState cannot have ExecuteApiFailureProceedState")
		}
		idlStOptions.ExecuteApiFailureProceedStateOptions =
			toIdlStateOptions(ShouldSkipWaitUntilAPI(stateOptions.ExecuteApiFailureProceedState), proceedStateOptions)
	}

	return idlStOptions
}
