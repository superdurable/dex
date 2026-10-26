// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import (
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	benchmarkWakeChannel    = dex.DefineChannel[int64]("benchmark-wake")
	benchmarkRPCCounter     = dex.DefineAttribute[int64]("benchmark-rpc-counter")
	benchmarkWriteAttribute = dex.DefineAttribute[int64]("benchmark_write", dex.SyncToAttributeStore())
	benchmarkReadAttributes = defineBenchmarkReadAttributes()
)

func defineBenchmarkReadAttributes() []dex.Attribute[string] {
	attributes := make([]dex.Attribute[string], 10)
	for index := range attributes {
		attributes[index] = dex.DefineAttribute[string](fmt.Sprintf("benchmark-read-%02d", index))
	}
	return attributes
}

type completionFlow struct{ dex.FlowDefaults }

func (completionFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(completionStep{})}
}

func (completionFlow) GetPersistenceSchema() dex.PersistenceSchema { return dex.PersistenceSchema{} }

type completionStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (completionStep) Execute(_ dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

type chainInput struct {
	Remaining          int
	PreviousAtUnixNano int64
}

type chainFlow struct {
	dex.FlowDefaults
	latencies *latencyCollector
	isAsync   bool
}

func (flow *chainFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(chainStep{latencies: flow.latencies, isAsync: flow.isAsync})}
}

func (*chainFlow) GetPersistenceSchema() dex.PersistenceSchema { return dex.PersistenceSchema{} }

type chainStep struct {
	dex.StepDefaultsNoWaitFor[chainInput]
	latencies *latencyCollector
	isAsync   bool
}

func (step chainStep) GetStepOptions() *dex.StepOptions {
	if step.isAsync {
		return &dex.StepOptions{ExecuteDurability: dex.StepDurabilityAsync}
	}
	return &dex.StepOptions{ExecuteDurability: dex.StepDurabilitySync}
}

func (step chainStep) Execute(_ dex.Context, input chainInput) (*dex.StepDecision, error) {
	now := time.Now()
	if input.PreviousAtUnixNano != 0 {
		step.latencies.add(time.Duration(now.UnixNano() - input.PreviousAtUnixNano))
	}
	if input.Remaining <= 1 {
		return dex.GracefulComplete(nil), nil
	}
	return dex.GoTo(step, chainInput{Remaining: input.Remaining - 1, PreviousAtUnixNano: now.UnixNano()}), nil
}

type rpcFlow struct{ dex.FlowDefaults }

func (rpcFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(rpcWaitStep{})}
}

func (flow rpcFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetCounter, nil),
		dex.DefineRPC(flow.IncrementCounter, &dex.RPCOptions{LockAttributes: []dex.AttributeLock{dex.LockAttribute(benchmarkRPCCounter)}}),
	}
}

func (rpcFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{benchmarkRPCCounter}, Channels: []dex.ChannelDef{benchmarkWakeChannel}}
}

func (rpcFlow) GetCounter(ctx dex.Context, _ dex.None) (*dex.RPCResult[int64], error) {
	value, err := benchmarkRPCCounter.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[int64]{Output: value}, nil
}

func (rpcFlow) IncrementCounter(ctx dex.Context, _ dex.None) (*dex.RPCResult[int64], error) {
	value, err := benchmarkRPCCounter.Get(ctx)
	if err != nil {
		return nil, err
	}
	value++
	if err := benchmarkRPCCounter.Set(ctx, value); err != nil {
		return nil, err
	}
	return &dex.RPCResult[int64]{Output: value}, nil
}

type rpcWaitStep struct{ dex.StepDefaults }

func (rpcWaitStep) WaitFor(_ dex.Context, _ dex.None) (*dex.Wait, error) {
	return dex.Until(benchmarkWakeChannel.ForOne()), nil
}

func (rpcWaitStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	values, err := benchmarkWakeChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(values) != 1 {
		return dex.ForceFail("expected one wake message"), nil
	}
	return dex.GracefulComplete(nil), nil
}

type wakeInput struct {
	Client   int
	Sequence int64
}

type wakeSignal struct {
	sequence  int64
	startedAt time.Time
}

// wakeSignals reports WaitFor returns and Execute starts to the in-process client of that Flow.
type wakeSignals struct {
	channels       []chan wakeSignal
	waitedChannels []chan int64
}

func newWakeSignals(clients int) *wakeSignals {
	signals := &wakeSignals{channels: make([]chan wakeSignal, clients), waitedChannels: make([]chan int64, clients)}
	for index := range signals.channels {
		signals.channels[index] = make(chan wakeSignal, 8)
		signals.waitedChannels[index] = make(chan int64, 8)
	}
	return signals
}

func (signals *wakeSignals) notifyWaited(input wakeInput) {
	if input.Client < 0 || input.Client >= len(signals.waitedChannels) {
		return
	}
	select {
	case signals.waitedChannels[input.Client] <- input.Sequence:
	default:
		// A retried WaitFor can report twice; the client discards stale sequences.
	}
}

func (signals *wakeSignals) notify(input wakeInput, startedAt time.Time) {
	if input.Client < 0 || input.Client >= len(signals.channels) {
		return
	}
	select {
	case signals.channels[input.Client] <- wakeSignal{sequence: input.Sequence, startedAt: startedAt}:
	default:
		// A retried Execute can report twice; the client discards stale sequences.
	}
}

type wakeFlow struct {
	dex.FlowDefaults
	signals *wakeSignals
}

func (flow *wakeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(wakeWaitStep{signals: flow.signals})}
}

func (*wakeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{benchmarkWakeChannel}}
}

type wakeWaitStep struct {
	dex.StepDefaults
	signals *wakeSignals
}

func (step wakeWaitStep) WaitFor(_ dex.Context, input wakeInput) (*dex.Wait, error) {
	step.signals.notifyWaited(input)
	return dex.Until(benchmarkWakeChannel.ForOne()), nil
}

func (step wakeWaitStep) Execute(ctx dex.Context, input wakeInput) (*dex.StepDecision, error) {
	startedAt := time.Now()
	step.signals.notify(input, startedAt)
	values, err := benchmarkWakeChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(values) != 1 {
		return dex.ForceFail("expected one wake message"), nil
	}
	return dex.GracefulComplete(nil), nil
}

// attributeFlow compares each read value with the value written at start, inside the Worker.
type attributeFlow struct {
	dex.FlowDefaults
	expectedValues map[string]string
}

func (*attributeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(rpcWaitStep{})}
}

func (flow *attributeFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.ReadAttributes, nil)}
}

func (*attributeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	definitions := make([]dex.AttributeDef, 0, len(benchmarkReadAttributes)+1)
	for _, attribute := range benchmarkReadAttributes {
		definitions = append(definitions, attribute)
	}
	definitions = append(definitions, benchmarkWriteAttribute)
	return dex.PersistenceSchema{Attributes: definitions, Channels: []dex.ChannelDef{benchmarkWakeChannel}}
}

// attributeReadOutput reports how many values matched their written content, and their total size.
type attributeReadOutput struct {
	VerifiedValues int
	VerifiedBytes  int64
}

func (flow *attributeFlow) ReadAttributes(ctx dex.Context, _ dex.None) (*dex.RPCResult[attributeReadOutput], error) {
	var output attributeReadOutput
	for _, attribute := range benchmarkReadAttributes {
		value, err := attribute.Get(ctx)
		if err != nil {
			return nil, err
		}
		expected := flow.expectedValues[attribute.AttributeName()]
		if value != expected {
			return nil, fmt.Errorf("Attribute %s returned %d bytes that differ from the %d bytes written", attribute.AttributeName(), len(value), len(expected))
		}
		output.VerifiedValues++
		output.VerifiedBytes += int64(len(value))
	}
	return &dex.RPCResult[attributeReadOutput]{Output: output}, nil
}
