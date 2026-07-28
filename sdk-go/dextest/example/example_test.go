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

package example

import (
	"testing"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dextest"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

var mockWfCtx *dextest.MockWorkflowContext
var mockPersistence *dextest.MockPersistence
var mockCommunication *dextest.MockCommunication
var emptyCmdResults = dex.CommandResults{}
var testCustomer = "customer1"
var emptyObj = dextest.NewTestObject(testCustomer)

func beforeEach(t *testing.T) {
	ctrl := gomock.NewController(t)

	mockWfCtx = dextest.NewMockWorkflowContext(ctrl)
	mockPersistence = dextest.NewMockPersistence(ctrl)
	mockCommunication = dextest.NewMockCommunication(ctrl)
}

func TestInitState_WaitUntil(t *testing.T) {
	beforeEach(t)

	state := NewInitState()

	mockPersistence.EXPECT().SetDataAttribute(keyCustomer, testCustomer)
	cmdReq, err := state.WaitUntil(mockWfCtx, emptyObj, mockPersistence, mockCommunication)
	assert.Nil(t, err)
	assert.Equal(t, dex.EmptyCommandRequest(), cmdReq)
}

func TestInitState_Execute(t *testing.T) {
	beforeEach(t)

	state := NewInitState()
	input := dextest.NewTestObject(testCustomer)

	decision, err := state.Execute(mockWfCtx, input, emptyCmdResults, mockPersistence, mockCommunication)
	assert.Nil(t, err)
	assert.Equal(t, dex.GracefulCompletingWorkflow, decision)
}
