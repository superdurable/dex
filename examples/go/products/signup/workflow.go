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
	Form           = dex.DefineAttribute[SignupForm]("Form")
	Status         = dex.DefineAttribute[string]("Status")
	VerifyEmail    = dex.DefineChannel[dex.None]("VerifyEmail")
	Task1Completed = dex.DefineChannel[dex.None]("Task1Completed")
	Task2Completed = dex.DefineChannel[dex.None]("Task2Completed")
)

const (
	StatusWaitingForVerification = "waiting_for_verification"
	StatusWaitingForTask1        = "waiting_for_task_1"
	StatusWaitingForTask2        = "waiting_for_task_2"
	StatusCompleted              = "completed"
)

type SignupForm struct {
	Username  string
	Email     string
	FirstName string
	LastName  string
}

type UserOnboardingFlow struct {
	dex.FlowDefaults
	service service.MyService
}

func NewUserOnboardingFlow(applicationService service.MyService) *UserOnboardingFlow {
	return &UserOnboardingFlow{service: applicationService}
}

func (flow *UserOnboardingFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(submitStep{service: flow.service}),
		dex.DefineStep(verifyEmailStep{service: flow.service}),
		dex.DefineStep(accomplishTask1Step{service: flow.service}),
		dex.DefineStep(accomplishTask2Step{service: flow.service}),
	}
}

func (*UserOnboardingFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{Form, Status},
		Channels:   []dex.ChannelDef{VerifyEmail, Task1Completed, Task2Completed},
	}
}

func (*UserOnboardingFlow) Verify(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[string], error) {
	status, err := Status.Get(ctx)
	if err != nil {
		return nil, err
	}
	if status != StatusWaitingForVerification {
		return &dex.RPCResult[string]{Output: "already verified"}, nil
	}
	if err := VerifyEmail.Publish(ctx, nil); err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: "verified"}, nil
}

func (*UserOnboardingFlow) AccomplishTask1(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[string], error) {
	status, err := Status.Get(ctx)
	if err != nil {
		return nil, err
	}
	if status != StatusWaitingForTask1 {
		return &dex.RPCResult[string]{Output: "task 1 is not waiting"}, nil
	}
	if err := Task1Completed.Publish(ctx, nil); err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: "task 1 accomplished"}, nil
}

func (*UserOnboardingFlow) AccomplishTask2(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[string], error) {
	status, err := Status.Get(ctx)
	if err != nil {
		return nil, err
	}
	if status != StatusWaitingForTask2 {
		return &dex.RPCResult[string]{Output: "task 2 is not waiting"}, nil
	}
	if err := Task2Completed.Publish(ctx, nil); err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: "task 2 accomplished"}, nil
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
	if err := Status.Set(ctx, StatusWaitingForVerification); err != nil {
		return nil, err
	}
	step.service.SendEmail(input.Email, "verify your email", "start your onboarding")
	return dex.GoTo(verifyEmailStep{}, nil), nil
}

type verifyEmailStep struct {
	dex.StepDefaults
	service service.MyService
}

func (verifyEmailStep) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.AnyOf(dex.Timer(24*time.Second), VerifyEmail.ForOne()), nil
}

func (step verifyEmailStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	form, err := Form.Get(ctx)
	if err != nil {
		return nil, err
	}
	results, err := VerifyEmail.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(results) > 0 {
		if err := Status.Set(ctx, StatusWaitingForTask1); err != nil {
			return nil, err
		}
		step.service.SendEmail(form.Email, "complete onboarding task 1", "task 1 is ready")
		return dex.GoTo(accomplishTask1Step{}, nil), nil
	}
	step.service.SendEmail(form.Email, "verification reminder", "please verify your email")
	return dex.GoTo(verifyEmailStep{}, nil), nil
}

type accomplishTask1Step struct {
	dex.StepDefaults
	service service.MyService
}

func (accomplishTask1Step) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.AnyOf(dex.Timer(24*time.Second), Task1Completed.ForOne()), nil
}

func (step accomplishTask1Step) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	form, err := Form.Get(ctx)
	if err != nil {
		return nil, err
	}
	results, err := Task1Completed.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(results) > 0 {
		if err := Status.Set(ctx, StatusWaitingForTask2); err != nil {
			return nil, err
		}
		step.service.SendEmail(form.Email, "complete onboarding task 2", "task 2 is ready")
		return dex.GoTo(accomplishTask2Step{}, nil), nil
	}
	step.service.SendEmail(form.Email, "task 1 reminder", "please complete onboarding task 1")
	return dex.GoTo(accomplishTask1Step{}, nil), nil
}

type accomplishTask2Step struct {
	dex.StepDefaults
	service service.MyService
}

func (accomplishTask2Step) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.AnyOf(dex.Timer(24*time.Second), Task2Completed.ForOne()), nil
}

func (step accomplishTask2Step) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	form, err := Form.Get(ctx)
	if err != nil {
		return nil, err
	}
	results, err := Task2Completed.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(results) > 0 {
		if err := Status.Set(ctx, StatusCompleted); err != nil {
			return nil, err
		}
		step.service.SendEmail(form.Email, "onboarding complete", "welcome aboard")
		return dex.GracefulComplete("onboarding completed"), nil
	}
	step.service.SendEmail(form.Email, "task 2 reminder", "please complete onboarding task 2")
	return dex.GoTo(accomplishTask2Step{}, nil), nil
}

var _ dex.Flow = (*UserOnboardingFlow)(nil)
