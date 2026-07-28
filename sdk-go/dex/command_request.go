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

import "github.com/superdurable/dex/sdk-go/gen/dexpb"

type CommandRequest struct {
	Commands            []Command
	CommandCombinations []dexpb.CommandCombination
	CommandWaitingType  dexpb.CommandWaitingType
}

// EmptyCommandRequest will jump to decide stage immediately.
func EmptyCommandRequest() *CommandRequest {
	return &CommandRequest{
		CommandWaitingType: dexpb.ALL_COMPLETED,
	}
}

// AnyCommandCompletedRequest will wait for all the commands to complete
func AnyCommandCompletedRequest(commands ...Command) *CommandRequest {
	return &CommandRequest{
		Commands:           commands,
		CommandWaitingType: dexpb.ANY_COMPLETED,
	}
}

// AllCommandsCompletedRequest will wait for any the commands to complete
func AllCommandsCompletedRequest(commands ...Command) *CommandRequest {
	return &CommandRequest{
		Commands:           commands,
		CommandWaitingType: dexpb.ALL_COMPLETED,
	}
}

// AnyCommandCombinationsCompletedRequest will wait for any combination to complete.
// Using this requires every command has a commandId when created.
// Functionally this one can cover both forAllCommandCompleted, forAnyCommandCompleted. So the other two are like "shortcuts" of it.
func AnyCommandCombinationsCompletedRequest(listsOfCommandIds [][]string, commands ...Command) *CommandRequest {
	for _, cmd := range commands {
		if cmd.CommandId == "" {
			panic("commandId must be provided for using ANY_COMMAND_COMBINATION_COMPLETED")
		}
	}

	var comList []dexpb.CommandCombination
	for _, commandIds := range listsOfCommandIds {
		com := dexpb.CommandCombination{
			CommandIds: commandIds,
		}
		comList = append(comList, com)
	}
	return &CommandRequest{
		Commands:            commands,
		CommandCombinations: comList,
		CommandWaitingType:  dexpb.ANY_COMBINATION_COMPLETED,
	}
}
