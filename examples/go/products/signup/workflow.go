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

package signup

import (
	"time"

	"github.com/superdurable/dex/examples/go/shared/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	Form   = dex.DefineAttribute[SignupForm]("Form")
	Status = dex.DefineAttribute[string]("Status")
	Verify = dex.DefineChannel[dex.None]("Verify")
)

type SignupForm struct {
	Username  string
	Email     string
	FirstName string
	LastName  string
}

type UserSignupFlow struct {
	dex.FlowDefaults
	service service.MyService
}

func NewUserSignupFlow(applicationService service.MyService) *UserSignupFlow {
	return &UserSignupFlow{service: applicationService}
}

func (flow *UserSignupFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(submitStep{service: flow.service}),
		dex.DefineStep(verifyStep{service: flow.service}),
	}
}

func (*UserSignupFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{Form, Status},
		Channels:   []dex.ChannelDef{Verify},
	}
}

func (*UserSignupFlow) Verify(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[string], error) {
	status, err := Status.Get(ctx)
	if err != nil {
		return nil, err
	}
	if status == "verified" {
		return &dex.RPCResult[string]{Output: "already verified"}, nil
	}
	if err := Status.Set(ctx, "verified"); err != nil {
		return nil, err
	}
	if err := Verify.Publish(ctx, nil); err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: "done"}, nil
}

type submitStep struct {
	dex.StepDefaultsNoWaitFor[SignupForm]
	service service.MyService
}

func (step submitStep) Execute(
	ctx dex.Context,
	input SignupForm,
) (*dex.StepDecision, error) {
	if err := Form.Set(ctx, input); err != nil {
		return nil, err
	}
	if err := Status.Set(ctx, "waiting"); err != nil {
		return nil, err
	}
	step.service.SendEmail(input.Email, "please verify the signup", "content")
	return dex.GoTo(verifyStep{}, nil), nil
}

type verifyStep struct {
	dex.StepDefaults
	service service.MyService
}

func (verifyStep) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.AnyOf(dex.Timer(24*time.Second), Verify.ForOne()), nil
}

func (step verifyStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	form, err := Form.Get(ctx)
	if err != nil {
		return nil, err
	}
	results, err := Verify.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(results) > 0 {
		step.service.SendEmail(form.Email, "welcome", "welcome to Indeed!")
		return dex.GracefulComplete("done"), nil
	}
	step.service.SendEmail(form.Email, "reminder", "please verify your email")
	return dex.GoTo(verifyStep{}, nil), nil
}

var _ dex.Flow = (*UserSignupFlow)(nil)
