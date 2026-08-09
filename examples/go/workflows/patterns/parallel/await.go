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
	"math/rand"
	"time"

	patternsservice "github.com/superdurable/dex/examples/go/workflows/patterns/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

const NotifyChannelName = "test_notify_channel"

var NotifyChannel = dex.DefineChannel[string](NotifyChannelName)

type ParallelStatesWithAwaitFlow struct {
	dex.FlowDefaults
	service patternsservice.ServiceDependency
}

func NewParallelStatesWithAwaitFlow(
	service patternsservice.ServiceDependency,
) *ParallelStatesWithAwaitFlow {
	return &ParallelStatesWithAwaitFlow{service: service}
}

func (*ParallelStatesWithAwaitFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(startingStep{}),
		dex.DefineStep(notifyUserStep{}),
		dex.DefineStep(awaitAllUsersNotifiedStep{}),
	}
}

func (*ParallelStatesWithAwaitFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{NotifyChannel},
	}
}

type startingStep struct {
	dex.StepDefaultsNoWaitFor[int]
}

func (startingStep) Execute(
	ctx dex.Context,
	countOfJobSeekers int,
) (*dex.StepDecision, error) {
	movements := make([]dex.StepMovement, 0, countOfJobSeekers+1)
	movements = append(
		movements,
		dex.MovementOf(awaitAllUsersNotifiedStep{}, countOfJobSeekers),
	)
	for index := 1; index <= countOfJobSeekers; index++ {
		movements = append(movements, dex.MovementOf(
			notifyUserStep{},
			JobSeeker{
				ID:          fmt.Sprintf("%d", index),
				Email:       "jobseeker@indeed.com",
				PhoneNumber: "0987654321",
			},
		))
	}
	return dex.GoToMulti(movements...), nil
}

type notifyUserStep struct {
	dex.StepDefaultsNoWaitFor[JobSeeker]
}

func (notifyUserStep) Execute(
	ctx dex.Context,
	jobSeeker JobSeeker,
) (*dex.StepDecision, error) {
	time.Sleep(time.Duration(rand.Intn(5000)) * time.Millisecond)
	message := "[FAKE] Notifying user of something: " + jobSeeker.ID
	fmt.Println(message)
	if err := ctx.RecordEvent("notification", message); err != nil {
		return nil, err
	}
	if err := NotifyChannel.Publish(ctx, "I sent something"); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type awaitAllUsersNotifiedStep struct {
	dex.StepDefaults
}

func (awaitAllUsersNotifiedStep) WaitFor(
	_ dex.Context,
	countOfJobSeekers int,
) (*dex.Wait, error) {
	return dex.Until(NotifyChannel.ForN(countOfJobSeekers)), nil
}

func (awaitAllUsersNotifiedStep) Execute(
	ctx dex.Context,
	countOfJobSeekers int,
) (*dex.StepDecision, error) {
	message := fmt.Sprintf("[FAKE] Sent all %d notifications", countOfJobSeekers)
	if err := ctx.RecordEvent("sent-notifications", message); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

var _ dex.Flow = (*ParallelStatesWithAwaitFlow)(nil)
