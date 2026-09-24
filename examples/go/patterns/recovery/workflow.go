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

package recovery

import (
	"fmt"
	"math/rand"

	"github.com/superdurable/dex/sdk-go/dex"
)

const WorkflowInputKey = "workflow-input-data-attribute-key"

var WorkflowInput = dex.DefineAttribute[FailureRecoveryWorkflowInput](WorkflowInputKey)

type FailureRecoveryWorkflowInput struct {
	ItemName          string
	RequestedQuantity int
}

type FailureRecoveryFlow struct {
	dex.FlowDefaults
	database         databaseConnection
	paymentProcessor paymentProcessor
}

func NewFailureRecoveryFlow() *FailureRecoveryFlow {
	return &FailureRecoveryFlow{
		database:         databaseConnection{},
		paymentProcessor: paymentProcessor{},
	}
}

func (flow *FailureRecoveryFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(updateItemQuantity{database: flow.database}),
		dex.DefineStep(chargeForItems{
			database:         flow.database,
			paymentProcessor: flow.paymentProcessor,
		}),
		dex.DefineStep(updateQuantityRecovery{database: flow.database}),
		dex.DefineStep(voidPaymentRecovery{
			database:         flow.database,
			paymentProcessor: flow.paymentProcessor,
		}),
	}
}

func (*FailureRecoveryFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{WorkflowInput},
	}
}

type updateItemQuantity struct {
	dex.DefaultStepType
	dex.NoWaitFor[FailureRecoveryWorkflowInput]
	database databaseConnection
}

func (updateItemQuantity) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteFailure: dex.ProceedToOnExecuteFailure(updateQuantityRecovery{}, nil),
		ExecuteRetry:   &dex.RetryPolicy{MaximumAttempts: 5},
	}
}

func (step updateItemQuantity) Execute(
	ctx dex.Context,
	input FailureRecoveryWorkflowInput,
) (*dex.StepDecision, error) {
	if err := WorkflowInput.Set(ctx, input); err != nil {
		return nil, err
	}
	if err := step.database.reduceQuantity(input.ItemName, input.RequestedQuantity); err != nil {
		return nil, err
	}
	return dex.GoTo(chargeForItems{}, input.RequestedQuantity), nil
}

type chargeForItems struct {
	dex.DefaultStepType
	dex.NoWaitFor[int]
	database         databaseConnection
	paymentProcessor paymentProcessor
}

func (chargeForItems) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteFailure: dex.ProceedToOnExecuteFailure(voidPaymentRecovery{}, nil),
		ExecuteRetry:   &dex.RetryPolicy{MaximumAttempts: 5},
	}
}

func (step chargeForItems) Execute(
	ctx dex.Context,
	_ int,
) (*dex.StepDecision, error) {
	input, err := WorkflowInput.Get(ctx)
	if err != nil {
		return nil, err
	}
	itemValue := step.database.getItemPrice(input.ItemName)
	orderValue := float64(input.RequestedQuantity) * itemValue
	if err := step.paymentProcessor.processPayment(orderValue); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

type updateQuantityRecovery struct {
	dex.StepDefaultsNoWaitFor[FailureRecoveryWorkflowInput]
	database databaseConnection
}

func (step updateQuantityRecovery) Execute(
	ctx dex.Context,
	input FailureRecoveryWorkflowInput,
) (*dex.StepDecision, error) {
	step.database.increaseQuantity(input.ItemName, input.RequestedQuantity)
	return dex.ForceFail("Failed to process transaction"), nil
}

type voidPaymentRecovery struct {
	dex.StepDefaultsNoWaitFor[int]
	database         databaseConnection
	paymentProcessor paymentProcessor
}

func (step voidPaymentRecovery) Execute(
	ctx dex.Context,
	_ int,
) (*dex.StepDecision, error) {
	workflow, err := WorkflowInput.Get(ctx)
	if err != nil {
		return nil, err
	}
	itemValue := step.database.getItemPrice(workflow.ItemName)
	orderValue := float64(workflow.RequestedQuantity) * itemValue
	step.paymentProcessor.voidPayment(orderValue)
	return dex.GoTo(updateQuantityRecovery{}, workflow), nil
}

type databaseConnection struct{}

func (databaseConnection) reduceQuantity(_ string, quantity int) error {
	fmt.Printf("Reducing quantity: %d\n", quantity)
	if quantity > rand.Intn(10) {
		return fmt.Errorf("not enough items available")
	}
	return nil
}

func (databaseConnection) increaseQuantity(_ string, quantity int) {
	fmt.Printf("Increasing quantity: %d\n", quantity)
}

func (databaseConnection) getItemPrice(_ string) float64 {
	return 3.14
}

type paymentProcessor struct{}

func (paymentProcessor) processPayment(_ float64) error {
	return fmt.Errorf("Payment could not be processed")
}

func (paymentProcessor) voidPayment(price float64) {
	fmt.Printf("Voiding payment for $ %.2f\n", price)
}

var _ dex.Flow = (*FailureRecoveryFlow)(nil)
