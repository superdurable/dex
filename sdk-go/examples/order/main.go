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

package main

import (
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	OrderStatus = dex.DefineAttribute[string](
		"order-status",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}),
	)
	Commands = dex.DefineChannel[Command]("commands")
)

type Command struct {
	Name string
}

type OrderInput struct {
	OrderID string
}

type OrderSnapshot struct {
	OrderID string
}

type WaitForCommandStep struct {
	dex.DefaultStepOptions
}

func (WaitForCommandStep) GetStepType() string {
	return "wait-for-command"
}

func (WaitForCommandStep) WaitFor(
	ctx dex.Context,
	input OrderInput,
) (dex.Wait, error) {
	if err := OrderStatus.Set(ctx, "waiting"); err != nil {
		return dex.Wait{}, err
	}

	if err := ctx.SetStepExecutionLocal(
		"snapshot",
		OrderSnapshot{OrderID: input.OrderID},
	); err != nil {
		return dex.Wait{}, err
	}
	if err := ctx.RecordEvent("waiting-for-command", input); err != nil {
		return dex.Wait{}, err
	}

	return dex.AnyOf(
		Commands.ForOne(dex.WithConditionID("command")),
		dex.Timer("timeout", 30*time.Minute),
	), nil
}

func (WaitForCommandStep) Execute(
	ctx dex.Context,
	input OrderInput,
) (dex.StepDecision, error) {
	if ctx.HasTimerFired() {
		return dex.ForceFail("command timed out"), nil
	}

	commands, err := Commands.GetConditionResults(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	if len(commands) == 0 {
		return dex.StepDecision{}, fmt.Errorf("command is missing")
	}

	var snapshot OrderSnapshot
	found, err := ctx.GetStepExecutionLocal("snapshot", &snapshot)
	if err != nil {
		return dex.StepDecision{}, err
	}
	if !found {
		return dex.StepDecision{}, fmt.Errorf("snapshot is missing")
	}
	return dex.GracefulComplete(snapshot), nil
}

var WaitForCommand = WaitForCommandStep{}
var _ dex.Step[OrderInput] = WaitForCommand

type OrderFlow struct{}

func (OrderFlow) GetFlowType() string {
	return "order"
}

func (OrderFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDefinition{OrderStatus},
		Channels:   []dex.ChannelDefinition{Commands},
	}
}

var Orders = OrderFlow{}
var _ dex.Flow = Orders

func main() {}
