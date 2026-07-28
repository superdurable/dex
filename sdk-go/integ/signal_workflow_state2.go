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

package integ

import (
	"fmt"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"time"
)

type signalWorkflowState2 struct {
	dex.WorkflowStateDefaults
}

const timerCommandId = "timerId"
const signalCommandId = "s1"

func (b signalWorkflowState2) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	var val int
	input.Get(&val)
	if val != 10 {
		panic(fmt.Sprintf("input value should be 10 but is %v", val))
	}

	return dex.AnyCommandCombinationsCompletedRequest(
		[][]string{
			{signalCommandId, timerCommandId},
		},
		dex.NewSignalCommand(signalCommandId, testChannelName1),
		dex.NewSignalCommand(signalCommandId, testChannelName2),
		dex.NewTimerCommandByDuration(timerCommandId, 24*time.Hour),
	), nil
}

func (b signalWorkflowState2) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	signal0 := commandResults.Signals[0]
	signal1 := commandResults.Signals[1]
	timer := commandResults.Timers[0]

	if signal0.CommandId != signalCommandId || signal0.ChannelName != testChannelName1 || signal0.Status != dexpb.RECEIVED {
		panic(testChannelName1 + " should be waiting....")
	}

	if signal1.CommandId != signalCommandId || signal1.ChannelName != testChannelName2 || signal1.Status != dexpb.WAITING {
		panic(testChannelName2 + " should be received....")
	}

	if timer.CommandId != timerCommandId || timer.Status != dexpb.FIRED {
		panic("timer should be fired")
	}

	var val int
	signal0.SignalValue.Get(&val)
	if val != 100 {
		panic("signal value should be 100")
	}

	return dex.GracefulCompleteWorkflow(val), nil
}
