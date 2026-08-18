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

package parallel

import (
	"fmt"

	patternsservice "github.com/superdurable/dex/examples/go/patterns/shared/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

type SimpleParallelStatesFlow struct {
	dex.FlowDefaults
	service patternsservice.ServiceDependency
}

func NewSimpleParallelStatesFlow(
	service patternsservice.ServiceDependency,
) *SimpleParallelStatesFlow {
	return &SimpleParallelStatesFlow{service: service}
}

func (*SimpleParallelStatesFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(initStep{}),
		dex.DefineStep(sendTextMessageStep{}),
		dex.DefineStep(sendEmailStep{}),
	}
}

func (*SimpleParallelStatesFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type initStep struct {
	dex.StepDefaultsNoWaitFor[JobSeeker]
}

func (initStep) Execute(
	ctx dex.Context,
	input JobSeeker,
) (*dex.StepDecision, error) {
	return dex.GoToMulti(
		dex.MovementOf(sendTextMessageStep{}, input.PhoneNumber),
		dex.MovementOf(sendEmailStep{}, input.Email),
	), nil
}

type sendTextMessageStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (sendTextMessageStep) Execute(
	ctx dex.Context,
	input string,
) (*dex.StepDecision, error) {
	message := "[FAKE] Sending text message to: " + input
	fmt.Println(message)
	if err := ctx.RecordEvent("text-message", message); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

type sendEmailStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (sendEmailStep) Execute(
	ctx dex.Context,
	input string,
) (*dex.StepDecision, error) {
	message := "[FAKE] Sending email to: " + input
	fmt.Println(message)
	if err := ctx.RecordEvent("email-notification", message); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

var _ dex.Flow = (*SimpleParallelStatesFlow)(nil)
