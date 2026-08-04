// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

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
