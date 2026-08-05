// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/blobcache"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"google.golang.org/grpc/codes"
)

var (
	statusAttribute = dex.DefineAttribute[string](
		"status",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}),
	)
	itemsAttribute   = dex.DefineAttributeMap[int]("items")
	commandChannel   = dex.DefineChannel[command]("commands")
	commandByOrder   = dex.DefineChannelMap[command]("commands-by-order")
	noPayloadChannel = dex.DefineChannel[dex.None]("no-payload")
)

type command struct {
	Name string
}

type stepInput struct {
	OrderID string
}

type noPayloadStep struct {
	dex.StepDefaults[dex.None]
}

func (noPayloadStep) GetStepType() string {
	return "no-payload"
}

func (noPayloadStep) Execute(
	dex.Context,
	dex.None,
) (dex.StepDecision, error) {
	return dex.DeadEnd(), nil
}

var noPayload = noPayloadStep{}
var _ dex.Step[dex.None] = noPayload

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
		dex.DefineStartStep(waitForCommand),
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
			noPayloadChannel,
		},
	}
}

func (contractFlow) Update(
	ctx dex.Context,
	input stepInput,
) (dex.RPCResult[command], error) {
	return dex.RPCResult[command]{
		Output:    command{Name: "updated"},
		NextSteps: []dex.StepMovement{dex.MovementOf(executeOnly, input)},
	}, nil
}

func (contractFlow) Describe(
	dex.Context,
	dex.None,
) (dex.RPCResult[command], error) {
	return dex.RPCResult[command]{Output: command{Name: "described"}}, nil
}

var flow = contractFlow{}
var _ dex.Flow = flow
var _ dex.RPC[stepInput, command] = flow.Update
var _ dex.RPC[dex.None, command] = flow.Describe

func TestPublicContractsCompile(t *testing.T) {
	_ = dex.MovementOf(noPayload, nil)
	_ = noPayloadChannel.ForOne()

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

	registry, err := dex.NewRegistry([]dex.Flow{flow})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cache, err := blobcache.New(&blobcache.Config{
		Dir:      t.TempDir(),
		MaxBytes: 1 << 20,
		Logger:   logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cache.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	if worker.WorkerTarget().Address != "localhost:8803" {
		t.Fatal("worker target was not derived from the bind address")
	}
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{
		WorkerTarget: worker.WorkerTarget(),
		Logger:       logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := worker.Stop(context.Background()); err != nil {
		t.Fatal(err)
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
	dex.ChannelDef,
	...any,
) error = (*dex.Client).PublishToChannel

var _ func(
	*dex.Client,
	context.Context,
	string,
	dex.ChannelDef,
	string,
	...any,
) error = (*dex.Client).PublishToChannelMap

var _ func(
	*dex.Client,
	context.Context,
	string,
	dex.AttributeDef,
	any,
) (bool, error) = (*dex.Client).GetAttribute

var _ func(
	*dex.Client,
	context.Context,
	string,
	dex.AttributeDef,
	string,
	any,
) (bool, error) = (*dex.Client).GetAttributeMap

var _ func(
	*dex.Client,
	context.Context,
	string,
	dex.AttributeDef,
	any,
) error = (*dex.Client).SetAttribute

var _ func(
	*dex.Client,
	context.Context,
	string,
	dex.AttributeDef,
	string,
	any,
) error = (*dex.Client).SetAttributeMap

var _ func(
	*dex.Client,
	context.Context,
	string,
	any,
	any,
	any,
	dex.InvokeOptions,
) error = (*dex.Client).InvokeRPC
