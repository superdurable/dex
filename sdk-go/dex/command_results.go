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

type (
	CommandResults struct {
		Timers                  []TimerCommandResult
		Signals                 []SignalCommandResult
		InternalChannelCommands []InternalChannelCommandResult
		WaitUntilApiSucceeded   *bool
	}

	SignalCommandResult struct {
		CommandId   string
		ChannelName string
		SignalValue Object
		Status      dexpb.ChannelRequestStatus
	}

	TimerCommandResult struct {
		CommandId string
		Status    dexpb.TimerStatus
	}

	InternalChannelCommandResult struct {
		CommandId   string
		ChannelName string
		Value       Object
		Status      dexpb.ChannelRequestStatus
	}
)

func (c CommandResults) GetTimerCommandResultById(id string) *TimerCommandResult {
	for _, cmd := range c.Timers {
		if cmd.CommandId == id {
			return &cmd
		}
	}
	return nil
}

func (c CommandResults) GetSignalCommandResultById(id string) *SignalCommandResult {
	for _, cmd := range c.Signals {
		if cmd.CommandId == id {
			return &cmd
		}
	}
	return nil
}

func (c CommandResults) GetInternalChannelCommandResultById(id string) *InternalChannelCommandResult {
	for _, cmd := range c.InternalChannelCommands {
		if cmd.CommandId == id {
			return &cmd
		}
	}
	return nil
}

func (c CommandResults) GetSignalCommandResultByChannel(channelName string) *SignalCommandResult {
	for _, cmd := range c.Signals {
		if cmd.ChannelName == channelName {
			return &cmd
		}
	}
	return nil
}

func (c CommandResults) GetInternalChannelCommandResultByChannel(channelName string) *InternalChannelCommandResult {
	for _, cmd := range c.InternalChannelCommands {
		if cmd.ChannelName == channelName {
			return &cmd
		}
	}
	return nil
}

func (c CommandResults) GetWaitUntilApiSucceeded() *bool {
	return c.WaitUntilApiSucceeded
}
