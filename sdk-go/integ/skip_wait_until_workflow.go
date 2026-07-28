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

type skipWaitUntilWorkflow struct {
	dex.DefaultWorkflowType
	dex.EmptyPersistenceSchema
	dex.EmptyCommunicationSchema
}

func (b skipWaitUntilWorkflow) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.StartingStateDef(&skipWaitUntilState1{}),
		dex.NonStartingStateDef(&skipWaitUntilState2{}),
	}
}

type skipWaitUntilWorkflow2 struct {
	dex.DefaultWorkflowType
	dex.EmptyPersistenceSchema
	dex.EmptyCommunicationSchema
}

func (b skipWaitUntilWorkflow2) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.StartingStateDef(skipWaitUntilState1{}),
		dex.NonStartingStateDef(skipWaitUntilState2{}),
	}
}
