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

package dex_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"google.golang.org/grpc/codes"
)

var (
	statusAttribute = dex.DefineAttribute[string](
		"status",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}),
	)
	itemsAttribute = dex.DefineAttributeMap[int]("items")
	commandChannel = dex.DefineChannel[command]("commands")
	commandByOrder = dex.DefineChannelMap[command]("commands-by-order")
)

type command struct {
	Name string
}

type stepInput struct {
	OrderID string
}

type waitingStep struct {
	dex.DefaultStepOptions
}

func (waitingStep) GetStepType() string {
	return "waiting"
}

func (waitingStep) WaitFor(
	ctx dex.Context,
	input stepInput,
) (dex.Wait, error) {
	if err := statusAttribute.Set(ctx, input.OrderID); err != nil {
		return dex.Wait{}, err
	}
	return dex.AnyComboOf(
		dex.Combo(
			commandChannel.ForOne(dex.WithConditionID("command")),
			dex.Timer(
				time.Minute,
				dex.WithConditionID("timeout"),
			),
		),
	), nil
}

func (waitingStep) Execute(
	ctx dex.Context,
	input stepInput,
) (dex.StepDecision, error) {
	if ctx.WaitForMethodFailed() || ctx.HasTimerFiredByIndex(0) {
		return dex.ForceFail("wait failed"), nil
	}
	results, err := commandChannel.GetConditionResults(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	if len(results) == 0 {
		return dex.DeadEnd(), nil
	}
	return dex.GoTo(executeOnly, input), nil
}

var waitForCommand = waitingStep{}
var _ dex.Step[stepInput] = waitForCommand

type executeOnlyStep struct {
	dex.StepDefaults[stepInput]
}

func (executeOnlyStep) GetStepType() string {
	return "execute-only"
}

func (executeOnlyStep) Execute(
	ctx dex.Context,
	input stepInput,
) (dex.StepDecision, error) {
	first := dex.MovementOf(waitForCommand, input)
	second := dex.MovementOf(executeOnly, input)
	return dex.GoToMulti(first, second), nil
}

var executeOnly = executeOnlyStep{}
var _ dex.Step[stepInput] = executeOnly

type contractFlow struct{}

func (contractFlow) GetFlowType() string {
	return "contract"
}

func (contractFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStepAsStart(waitForCommand),
		dex.DefineStep(executeOnly),
	}
}

func (contractFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{
			statusAttribute,
			itemsAttribute,
		},
		Channels: []dex.ChannelDef{
			commandChannel,
			commandByOrder,
		},
	}
}

func (contractFlow) Update(
	ctx dex.Context,
	input stepInput,
) (dex.RPCResult[command], error) {
	return dex.ReplyAndMove(
		command{Name: "updated"},
		dex.MovementOf(executeOnly, input),
	), nil
}

var flow = contractFlow{}
var _ dex.Flow = flow
var _ dex.RPC[stepInput, command] = flow.Update

func TestPublicContractsCompile(t *testing.T) {
	initial, err := dex.InitialAttribute(statusAttribute, "new")
	if err != nil {
		t.Fatal(err)
	}
	mapInitial, err := dex.InitialAttributeMapValue(itemsAttribute, "order-1", 1)
	if err != nil {
		t.Fatal(err)
	}

	mode := dex.SearchAllActiveSteps
	durability := dex.StepDurabilitySync
	config := dex.FlowConfig{
		ActiveStepSearchMode:   &mode,
		ContinueAsNewThreshold: ptr.Any(int32(100)),
		StepDurability:         &durability,
	}
	options := dex.StartFlowOptions{
		Timeout:        ptr.Any(time.Minute),
		StartDelay:     ptr.Any(time.Second),
		Attributes:     []dex.InitialAttributeDef{initial, mapInitial},
		ConfigOverride: &config,
	}
	if options.Timeout == nil ||
		options.StartDelay == nil ||
		len(options.Attributes) != 2 {
		t.Fatal("start flow options are missing")
	}
}

func TestErrorSupportsErrorsAs(t *testing.T) {
	sdkError := error(&dex.Error{
		Code:      codes.NotFound,
		SubStatus: dex.ErrorFlowNotFound,
		Detail:    "flow is missing",
	})
	var target *dex.Error
	if !errors.As(sdkError, &target) {
		t.Fatal("SDK error does not support errors.As")
	}
	if target.Code != codes.NotFound {
		t.Fatalf("unexpected code: %s", target.Code)
	}
}

func compileAttributeOperations(ctx dex.Context) error {
	if _, _, err := statusAttribute.Get(ctx); err != nil {
		return err
	}
	if err := statusAttribute.Set(ctx, "ready"); err != nil {
		return err
	}
	if err := statusAttribute.Delete(ctx); err != nil {
		return err
	}
	if _, _, err := itemsAttribute.Get(ctx, "order-1"); err != nil {
		return err
	}
	if err := itemsAttribute.Set(ctx, "order-1", 1); err != nil {
		return err
	}
	return itemsAttribute.Delete(ctx, "order-1")
}

func compileChannelOperations(ctx dex.Context) error {
	if err := commandChannel.Publish(ctx, command{}); err != nil {
		return err
	}
	if err := commandByOrder.Publish(ctx, "order-1", command{}); err != nil {
		return err
	}
	_ = commandChannel.ForOne()
	_ = commandChannel.ForN(2)
	_ = commandChannel.AtLeast(1)
	_ = commandChannel.AtMost(2)
	_ = commandChannel.AtLeastAtMost(1, 2)
	_ = commandByOrder.ForOne("order-1")
	_ = commandByOrder.ForN("order-1", 2)
	_ = commandByOrder.AtLeast("order-1", 1)
	_ = commandByOrder.AtMost("order-1", 2)
	_ = commandByOrder.AtLeastAtMost("order-1", 1, 2)
	_ = commandChannel.Size(ctx)
	_ = commandByOrder.Size(ctx, "order-1")
	if _, err := commandChannel.GetConditionResults(ctx); err != nil {
		return err
	}
	_, err := commandByOrder.GetConditionResults(ctx, "order-1")
	return err
}

func compileContextOperations(ctx dex.Context) error {
	if err := ctx.SetStepExecutionLocal("snapshot", command{}); err != nil {
		return err
	}
	var snapshot command
	if _, err := ctx.GetStepExecutionLocal("snapshot", &snapshot); err != nil {
		return err
	}
	if err := ctx.RecordEvent("snapshot", snapshot); err != nil {
		return err
	}
	_ = ctx.HasTimerFired()
	_ = ctx.HasTimerFiredByIndex(0)
	_ = ctx.WaitForMethodFailed()
	return nil
}

var _ = dex.AllOf(
	commandChannel.ForOne(),
	dex.Timer(time.Minute, dex.WithConditionID("all")),
)
var _ = dex.AnyOf(
	commandChannel.ForOne(),
	dex.Timer(time.Minute, dex.WithConditionID("any")),
)
var _ = dex.ForceComplete("done")
var _ = dex.GracefulComplete("done")
var _ = dex.ForceFail("failed")
var _ = dex.DeadEnd()
var _ = dex.ForceCompleteOnChannelsEmpty(
	"done",
	[]dex.ChannelDef{commandChannel},
	dex.MovementOf(executeOnly, stepInput{}),
)

var _ func(
	*dex.Client,
	context.Context,
	dex.Flow,
	string,
	any,
	dex.StartFlowOptions,
) (string, error) = (*dex.Client).StartFlow

var _ func(
	*dex.Client,
	context.Context,
	string,
	string,
	dex.ChannelDef,
	...any,
) error = (*dex.Client).PublishToChannel

var _ func(
	*dex.Client,
	context.Context,
	string,
	string,
	dex.ChannelDef,
	string,
	...any,
) error = (*dex.Client).PublishToChannelMap

var _ func(
	*dex.Client,
	context.Context,
	string,
	string,
	dex.AttributeDef,
	any,
) (bool, error) = (*dex.Client).GetAttribute

var _ func(
	*dex.Client,
	context.Context,
	string,
	string,
	dex.AttributeDef,
	string,
	any,
) (bool, error) = (*dex.Client).GetAttributeMap

var _ func(
	*dex.Client,
	context.Context,
	string,
	string,
	dex.AttributeDef,
	any,
) error = (*dex.Client).SetAttribute

var _ func(
	*dex.Client,
	context.Context,
	string,
	string,
	dex.AttributeDef,
	string,
	any,
) error = (*dex.Client).SetAttributeMap

var _ func(
	*dex.Client,
	context.Context,
	string,
	string,
	any,
	any,
	any,
	dex.InvokeOptions,
) error = (*dex.Client).InvokeRPC
