// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package streamcontexts

import "github.com/superdurable/dex/sdk-go/dex"

var Activity = dex.DefineStream[string]("activity", 1024)

type ActivityFlow struct{ dex.FlowDefaults }

func (flow *ActivityFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(EmitActivityStep{flow: flow})}
}

func (flow *ActivityFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
		dex.DefineRPC(flow.SendActivity, nil),
		dex.DefineRPC(flow.SendDirectActivity, nil),
		dex.DefineRPC(flow.SendLoopActivity, nil),
		dex.DefineRPC(flow.CreateActivityWriter, nil),
		dex.DefineRPC(flow.SendBufferedActivity, nil),
	}
}

func (*ActivityFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Streams: []dex.StreamDef{Activity}}
}

// dex:group group-id:activity group-label:"Activity"
// dex:explanation text:"Emit activity from Step invocations."
type EmitActivityStep struct {
	dex.StepDefaults
	flow *ActivityFlow
}

func (step EmitActivityStep) WaitFor(ctx dex.Context, _ dex.None) (*dex.Wait, error) {
	if err := step.flow.writeActivitySnapshot(ctx); err != nil {
		return nil, err
	}
	if err := writeBufferedActivity(ctx, Activity); err != nil {
		return nil, err
	}
	return dex.SkipWaitImmediately(), nil
}

func (step EmitActivityStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := step.flow.writeActivitySnapshot(ctx); err != nil {
		return nil, err
	}
	if err := writeBufferedActivity(ctx, Activity); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

func (flow *ActivityFlow) SendActivity(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := flow.writeActivitySnapshot(ctx); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*ActivityFlow) SendDirectActivity(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := Activity.Write(ctx, "first"); err != nil {
		return nil, err
	}
	if err := Activity.Write(ctx, "second"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*ActivityFlow) SendLoopActivity(ctx dex.Context, messages []string) (*dex.RPCResult[dex.None], error) {
	for _, message := range messages {
		if err := writeStreamActivity(ctx, Activity, message); err != nil {
			return nil, err
		}
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*ActivityFlow) CreateActivityWriter(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if _, err := dex.NewBufferedTextStream(ctx, Activity); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*ActivityFlow) SendBufferedActivity(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := writeBufferedActivity(ctx, Activity); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (flow *ActivityFlow) HandleTimeout(ctx dex.Context) (*dex.StepDecision, error) {
	if err := flow.writeActivitySnapshot(ctx); err != nil {
		return nil, err
	}
	if _, err := dex.NewBufferedTextStream(ctx, Activity); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

func (*ActivityFlow) GetDexSummary(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*ActivityFlow) GetDexDisplay(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}
