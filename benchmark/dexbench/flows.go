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
}

func (flow *chainFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(chainStep{latencies: flow.latencies})}
}

func (*chainFlow) GetPersistenceSchema() dex.PersistenceSchema { return dex.PersistenceSchema{} }

type chainStep struct {
	dex.StepDefaultsNoWaitFor[chainInput]
	latencies *latencyCollector
}

func (chainStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteDurability: dex.StepDurabilitySync}
}

func (step chainStep) Execute(_ dex.Context, input chainInput) (*dex.StepDecision, error) {
	next, isDone := step.latencies.recordChainStep(input)
	if isDone {
		return dex.GracefulComplete(nil), nil
	}
	return dex.GoTo(step, next), nil
}

// asyncChainFlow is chainFlow with ASYNC Step durability.
type asyncChainFlow struct {
	dex.FlowDefaults
	latencies *latencyCollector
}

func (flow *asyncChainFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(asyncChainStep{latencies: flow.latencies})}
}

func (*asyncChainFlow) GetPersistenceSchema() dex.PersistenceSchema { return dex.PersistenceSchema{} }

type asyncChainStep struct {
	dex.StepDefaultsNoWaitFor[chainInput]
	latencies *latencyCollector
}

func (asyncChainStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteDurability: dex.StepDurabilityAsync}
}

func (step asyncChainStep) Execute(_ dex.Context, input chainInput) (*dex.StepDecision, error) {
	next, isDone := step.latencies.recordChainStep(input)
	if isDone {
		return dex.GracefulComplete(nil), nil
	}
	return dex.GoTo(step, next), nil
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
	Client int
}

// wakeSignal reports the start of a woken Execute and the sequence of the message it consumed.
type wakeSignal struct {
	sequence  int64
	startedAt time.Time
}

// wakeSignals reports WaitFor returns and Execute starts to the in-process client of that Flow.
type wakeSignals struct {
	channels       []chan wakeSignal
	waitedChannels []chan struct{}
}

func newWakeSignals(clients int) *wakeSignals {
	signals := &wakeSignals{channels: make([]chan wakeSignal, clients), waitedChannels: make([]chan struct{}, clients)}
	for index := range signals.channels {
		signals.channels[index] = make(chan wakeSignal, 8)
		signals.waitedChannels[index] = make(chan struct{}, 8)
	}
	return signals
}

func (signals *wakeSignals) notifyWaited(client int) {
	if client < 0 || client >= len(signals.waitedChannels) {
		return
	}
	select {
	case signals.waitedChannels[client] <- struct{}{}:
	default:
		// The client confirms the wait through GetFlowState, so a dropped notice only adds a poll.
	}
}

func (signals *wakeSignals) notify(client int, sequence int64, startedAt time.Time) {
	if client < 0 || client >= len(signals.channels) {
		return
	}
	select {
	case signals.channels[client] <- wakeSignal{sequence: sequence, startedAt: startedAt}:
	default:
		// The client matches signals by sequence and discards stale ones.
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
	step.signals.notifyWaited(input.Client)
	return dex.Until(benchmarkWakeChannel.ForOne()), nil
}

func (step wakeWaitStep) Execute(ctx dex.Context, input wakeInput) (*dex.StepDecision, error) {
	startedAt := time.Now()
	values, err := benchmarkWakeChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(values) != 1 {
		return dex.ForceFail("expected one wake message"), nil
	}
	step.signals.notify(input.Client, values[0], startedAt)
	// The run stays open: each wake moves to a new wait.
	return dex.GoTo(step, input), nil
}

// attributeFlow holds the Attributes that clients read directly from the server.
type attributeFlow struct{ dex.FlowDefaults }

func (*attributeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(rpcWaitStep{})}
}

func (*attributeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	definitions := make([]dex.AttributeDef, 0, len(benchmarkReadAttributes))
	for _, attribute := range benchmarkReadAttributes {
		definitions = append(definitions, attribute)
	}
	return dex.PersistenceSchema{Attributes: definitions, Channels: []dex.ChannelDef{benchmarkWakeChannel}}
}
