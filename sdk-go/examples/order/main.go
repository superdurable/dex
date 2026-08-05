// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/blobcache"
)

var (
	OrderStatus = dex.DefineAttribute[string](
		"order-status",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}),
	)
	ItemQuantities  = dex.DefineAttributeMap[int]("item-quantities")
	Commands        = dex.DefineChannel[Command]("commands")
	CommandsByOrder = dex.DefineChannelMap[Command]("commands-by-order")
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
	dex.StepDefaults
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
		dex.Timer(
			30*time.Minute,
			dex.WithConditionID("timeout"),
		),
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

type OrderFlow struct {
	dex.FlowDefaults
}

func (OrderFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(WaitForCommand),
	}
}

func (OrderFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{OrderStatus, ItemQuantities},
		Channels:   []dex.ChannelDef{Commands, CommandsByOrder},
	}
}

var Orders = OrderFlow{}
var _ dex.Flow = Orders

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	registry, err := dex.NewRegistry([]dex.Flow{Orders})
	if err != nil {
		logger.Error("register order Flow", "error", err)
		os.Exit(1)
	}
	cache, err := blobcache.New(&blobcache.Config{
		Dir:      "/var/tmp/dex-order-example-blobs",
		MaxBytes: 1 << 30,
		Logger:   logger,
	})
	if err != nil {
		logger.Error("create order blob cache", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := cache.Close(); err != nil {
			logger.Error("close order blob cache", "error", err)
		}
	}()
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		BindAddress: ":8803",
		Logger:      logger,
	})
	if err != nil {
		logger.Error("create order Worker", "error", err)
		os.Exit(1)
	}
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{
		WorkerTarget: worker.WorkerTarget(),
		Logger:       logger,
	})
	if err != nil {
		logger.Error("create order Client", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := client.Close(); err != nil {
			logger.Error("close order Client", "error", err)
		}
	}()

	ctx, stopSignals := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stopSignals()
	go stopOrderWorker(ctx, worker, logger)

	logger.Info("starting order Worker", "target", worker.WorkerTarget().Address)
	if err := worker.Start(); err != nil {
		logger.Error("run order Worker", "error", err)
		os.Exit(1)
	}
}

func stopOrderWorker(ctx context.Context, worker *dex.Worker, logger dex.Logger) {
	<-ctx.Done()
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := worker.Stop(stopCtx); err != nil {
		logger.Error("stop order Worker", "error", err)
	}
}
