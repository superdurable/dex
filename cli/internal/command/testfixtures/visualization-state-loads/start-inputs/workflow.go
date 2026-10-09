// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package startinputs

import (
	"context"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

type StartInput struct {
	Message string `json:"message"`
}
type StartInputFlow struct {
	dex.FlowDefaults
	client *dex.Client
}

func (flow *StartInputFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(BeginInputStep{client: flow.client})}
}
func (flow *StartInputFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.GetDexSummary, nil), dex.DefineRPC(flow.GetDexDisplay, nil)}
}
func (*StartInputFlow) GetPersistenceSchema() dex.PersistenceSchema { return dex.PersistenceSchema{} }
func (*StartInputFlow) GetDexSummary(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}
func (*StartInputFlow) GetDexDisplay(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

// dex:group group-id:start group-label:"Start"
// dex:explanation text:"Accept the typed start input."
type BeginInputStep struct {
	dex.StepDefaultsNoWaitFor[StartInput]
	client *dex.Client
}

func (step BeginInputStep) Execute(_ dex.Context, _ StartInput) (*dex.StepDecision, error) {
	if err := startMissingInput(context.Background(), step.client); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

func checkStartInputs(ctx context.Context, client *dex.Client, unknownInput any, unknownFlow dex.Flow) error {
	options := dex.StartFlowOptions{RequestID: ptr.Any("start-request")}
	if _, err := client.StartFlow(ctx, &StartInputFlow{}, "nil-input", nil, options); err != nil {
		return err
	}
	if _, err := client.StartFlow(ctx, &StartInputFlow{}, "string-input", "wrong type", options); err != nil {
		return err
	}
	if _, err := client.StartFlow(ctx, &StartInputFlow{}, "pointer-input", &StartInput{}, options); err != nil {
		return err
	}
	if _, err := client.StartFlow(ctx, &StartInputFlow{}, "valid-input", StartInput{}, options); err != nil {
		return err
	}
	if _, err := client.StartFlow(ctx, &StartInputFlow{}, "unknown-input", unknownInput, options); err != nil {
		return err
	}
	if _, err := client.StartFlow(ctx, unknownFlow, "unknown-flow", nil, options); err != nil {
		return err
	}
	if _, err := client.StartFlow(ctx, &NoInputFlow{}, "none-nil", nil, options); err != nil {
		return err
	}
	if _, err := client.StartFlow(ctx, &NoInputFlow{}, "none-value", struct{}{}, options); err != nil {
		return err
	}
	if _, err := client.StartFlow(ctx, &PointerInputFlow{}, "pointer-nil", nil, options); err != nil {
		return err
	}
	if _, err := client.StartFlow(ctx, &PointerInputFlow{}, "pointer-value", &StartInput{}, options); err != nil {
		return err
	}
	if _, err := client.StartFlow(ctx, &PointerInputFlow{}, "pointer-struct", StartInput{}, options); err != nil {
		return err
	}
	if _, err := client.StartFlow(ctx, &NoStartFlow{}, "no-start", nil, options); err != nil {
		return err
	}
	if _, err := client.StartFlow(ctx, &ConditionalInputFlow{}, "conditional-start", nil, options); err != nil {
		return err
	}
	return nil
}
