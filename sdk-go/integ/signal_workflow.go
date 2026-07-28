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

import "github.com/superdurable/dex/sdk-go/dex"

type signalWorkflow struct {
	dex.DefaultWorkflowType
	dex.EmptyPersistenceSchema
}

const testChannelName1 = "test-channel-name-1"
const testChannelName2 = "test-channel-name-2"

func (b signalWorkflow) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.StartingStateDef(&signalWorkflowState1{}),
		dex.NonStartingStateDef(&signalWorkflowState2{}),
	}
}

func (b signalWorkflow) GetCommunicationSchema() []dex.CommunicationMethodDef {
	return []dex.CommunicationMethodDef{
		dex.SignalChannelDef(testChannelName1),
		dex.SignalChannelDef(testChannelName2),
	}
}
