// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package subscription

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/superdurable/dex/examples/go/workflows/service"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dextest"
	"go.uber.org/mock/gomock"
)

// mockgen -source=workflows/subscription/my_service.go -destination=workflows/subscription/my_service_mock.go --package=subscription

var testCustomer = Customer{
	FirstName: "Quanzheng",
	LastName:  "Long",
	Id:        "123",
	Email:     "qlong.seattle@gmail.com",
	Subscription: Subscription{
		BillingPeriod:       time.Second,
		MaxBillingPeriods:   10,
		TrialPeriod:         time.Second * 2,
		BillingPeriodCharge: 100,
	},
}

var testCustomerObj = dextest.NewTestObject(testCustomer)

var mockWfCtx *dextest.MockWorkflowContext
var mockPersistence *dextest.MockPersistence
var mockCommunication *dextest.MockCommunication
var emptyCmdResults = dex.CommandResults{}
var emptyObj = dextest.NewTestObject(nil)
var mockSvc *service.MockMyService

func beforeEach(t *testing.T) {
	ctrl := gomock.NewController(t)

	mockSvc = service.NewMockMyService(ctrl)
	mockWfCtx = dextest.NewMockWorkflowContext(ctrl)
	mockPersistence = dextest.NewMockPersistence(ctrl)
	mockCommunication = dextest.NewMockCommunication(ctrl)
}

func TestInitState_WaitUntil(t *testing.T) {
	beforeEach(t)

	state := NewInitState()

	mockPersistence.EXPECT().SetDataAttribute(keyCustomer, testCustomer)
	cmdReq, err := state.WaitUntil(mockWfCtx, testCustomerObj, mockPersistence, mockCommunication)
	assert.Nil(t, err)
	assert.Equal(t, dex.EmptyCommandRequest(), cmdReq)
}

func TestInitState_Execute(t *testing.T) {
	beforeEach(t)

	state := NewInitState()
	input := dextest.NewTestObject(testCustomer)

	decision, err := state.Execute(mockWfCtx, input, emptyCmdResults, mockPersistence, mockCommunication)
	assert.Nil(t, err)
	assert.Equal(t, dex.MultiNextStates(
		trialState{}, cancelState{}, updateChargeAmountState{},
	), decision)
}

func TestTrialState_WaitUntil(t *testing.T) {
	beforeEach(t)

	state := NewTrialState(mockSvc)

	mockSvc.EXPECT().SendEmail(testCustomer.Email, gomock.Any(), gomock.Any())
	mockPersistence.EXPECT().GetDataAttribute(keyCustomer, gomock.Any()).SetArg(1, testCustomer)
	cmdReq, err := state.WaitUntil(mockWfCtx, emptyObj, mockPersistence, mockCommunication)
	assert.Nil(t, err)
	assert.Equal(t, dex.AllCommandsCompletedRequest(
		dex.NewTimerCommandByDuration("", testCustomer.Subscription.TrialPeriod),
	), cmdReq)
}

func TestTrialState_Execute(t *testing.T) {
	beforeEach(t)

	state := NewTrialState(mockSvc)

	mockPersistence.EXPECT().SetDataAttribute(keyBillingPeriodNum, 0)

	decision, err := state.Execute(mockWfCtx, emptyObj, emptyCmdResults, mockPersistence, mockCommunication)
	assert.Nil(t, err)
	assert.Equal(t, dex.SingleNextState(
		chargeCurrentBillState{}, nil,
	), decision)
}

func TestChargeCurrentBillStateStart_waitForDuration(t *testing.T) {
	beforeEach(t)

	state := NewChargeCurrentBillState(mockSvc)

	mockPersistence.EXPECT().GetDataAttribute(keyCustomer, gomock.Any()).SetArg(1, testCustomer)
	mockPersistence.EXPECT().GetDataAttribute(keyBillingPeriodNum, gomock.Any()).SetArg(1, 0)
	mockPersistence.EXPECT().SetDataAttribute(keyBillingPeriodNum, 1)

	cmdReq, err := state.WaitUntil(mockWfCtx, emptyObj, mockPersistence, mockCommunication)
	assert.Nil(t, err)
	assert.Equal(t, dex.AllCommandsCompletedRequest(
		dex.NewTimerCommandByDuration("", testCustomer.Subscription.BillingPeriod),
	), cmdReq)
}

func TestChargeCurrentBillStateStart_subscriptionOver(t *testing.T) {
	beforeEach(t)

	state := NewChargeCurrentBillState(mockSvc)

	mockPersistence.EXPECT().GetDataAttribute(keyCustomer, gomock.Any()).SetArg(1, testCustomer)
	mockPersistence.EXPECT().GetDataAttribute(keyBillingPeriodNum, gomock.Any()).SetArg(1, testCustomer.Subscription.MaxBillingPeriods)
	mockPersistence.EXPECT().SetStateExecutionLocal(subscriptionOverKey, true)

	cmdReq, err := state.WaitUntil(mockWfCtx, emptyObj, mockPersistence, mockCommunication)
	assert.Nil(t, err)
	assert.Equal(t, dex.EmptyCommandRequest(), cmdReq)
}

func TestChargeCurrentBillStateDecide_subscriptionNotOver(t *testing.T) {
	beforeEach(t)

	state := NewChargeCurrentBillState(mockSvc)

	mockPersistence.EXPECT().GetDataAttribute(keyCustomer, gomock.Any()).SetArg(1, testCustomer)
	mockPersistence.EXPECT().GetStateExecutionLocal(subscriptionOverKey, gomock.Any())
	mockSvc.EXPECT().ChargeUser(testCustomer.Email, testCustomer.Id, testCustomer.Subscription.BillingPeriodCharge)

	decision, err := state.Execute(mockWfCtx, emptyObj, emptyCmdResults, mockPersistence, mockCommunication)
	assert.Nil(t, err)
	assert.Equal(t, dex.SingleNextState(&chargeCurrentBillState{}, nil), decision)
}

func TestChargeCurrentBillStateDecide_subscriptionOver(t *testing.T) {
	beforeEach(t)

	state := NewChargeCurrentBillState(mockSvc)

	mockPersistence.EXPECT().GetDataAttribute(keyCustomer, gomock.Any()).SetArg(1, testCustomer)
	mockPersistence.EXPECT().GetStateExecutionLocal(subscriptionOverKey, gomock.Any()).SetArg(1, true)
	mockSvc.EXPECT().SendEmail(testCustomer.Email, gomock.Any(), gomock.Any())

	decision, err := state.Execute(mockWfCtx, emptyObj, emptyCmdResults, mockPersistence, mockCommunication)
	assert.Nil(t, err)
	assert.Equal(t, dex.ForceCompletingWorkflow, decision)
}

func TestUpdateChargeAmountState_WaitUntil(t *testing.T) {
	beforeEach(t)

	state := NewUpdateChargeAmountState()

	cmdReq, err := state.WaitUntil(mockWfCtx, emptyObj, mockPersistence, mockCommunication)
	assert.Nil(t, err)
	assert.Equal(t, dex.AllCommandsCompletedRequest(dex.NewSignalCommand("", SignalUpdateBillingPeriodChargeAmount)), cmdReq)
}

func TestUpdateChargeAmountState_Execute(t *testing.T) {
	beforeEach(t)

	state := NewUpdateChargeAmountState()

	cmdResults := dex.CommandResults{
		Signals: []dex.SignalCommandResult{
			{
				ChannelName: SignalUpdateBillingPeriodChargeAmount,
				SignalValue: dextest.NewTestObject(200),
				Status:      dexpb.RECEIVED,
			},
		},
	}

	updatedCustomer := testCustomer
	updatedCustomer.Subscription.BillingPeriodCharge = 200

	mockPersistence.EXPECT().GetDataAttribute(keyCustomer, gomock.Any()).SetArg(1, testCustomer)
	mockPersistence.EXPECT().SetDataAttribute(keyCustomer, updatedCustomer)

	decision, err := state.Execute(mockWfCtx, emptyObj, cmdResults, mockPersistence, mockCommunication)
	assert.Nil(t, err)
	assert.Equal(t, dex.SingleNextState(&updateChargeAmountState{}, nil), decision)
}

func TestCancelState_WaitUntil(t *testing.T) {
	beforeEach(t)

	state := NewCancelState(mockSvc)

	cmdReq, err := state.WaitUntil(mockWfCtx, emptyObj, mockPersistence, mockCommunication)
	assert.Nil(t, err)
	assert.Equal(t, dex.AllCommandsCompletedRequest(dex.NewSignalCommand("", SignalCancelSubscription)), cmdReq)
}

func TestCancelState_Execute(t *testing.T) {
	beforeEach(t)

	state := NewCancelState(mockSvc)

	mockPersistence.EXPECT().GetDataAttribute(keyCustomer, gomock.Any()).SetArg(1, testCustomer)
	mockSvc.EXPECT().SendEmail(testCustomer.Email, gomock.Any(), gomock.Any())

	decision, err := state.Execute(mockWfCtx, emptyObj, emptyCmdResults, mockPersistence, mockCommunication)
	assert.Nil(t, err)
	assert.Equal(t, dex.ForceCompletingWorkflow, decision)
}
