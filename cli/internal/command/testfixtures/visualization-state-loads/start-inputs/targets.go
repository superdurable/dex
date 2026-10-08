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

type NoInputFlow struct{ dex.FlowDefaults }

func (*NoInputFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(NoInputStep{})}
}

type NoInputStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (NoInputStep) Execute(_ dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

type PointerInputFlow struct{ dex.FlowDefaults }

func (*PointerInputFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(PointerInputStep{})}
}

type PointerInputStep struct {
	dex.StepDefaultsNoWaitFor[*StartInput]
}

func (PointerInputStep) Execute(_ dex.Context, _ *StartInput) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

type NoStartFlow struct{ dex.FlowDefaults }

func (*NoStartFlow) GetSteps() []dex.StepDef { return nil }

type ConditionalInputFlow struct {
	dex.FlowDefaults
	hasStartStep bool
}

func (flow *ConditionalInputFlow) GetSteps() []dex.StepDef {
	if flow.hasStartStep {
		return []dex.StepDef{dex.DefineStartStep(BeginInputStep{})}
	}
	return nil
}

func startMissingInput(ctx context.Context, client *dex.Client) error {
	_, err := client.StartFlow(ctx, &StartInputFlow{}, "helper-start", nil, dex.StartFlowOptions{RequestID: ptr.Any("helper-request")})
	return err
}

func (*NoInputFlow) GetPersistenceSchema() dex.PersistenceSchema { return dex.PersistenceSchema{} }

func (*PointerInputFlow) GetPersistenceSchema() dex.PersistenceSchema { return dex.PersistenceSchema{} }

func (*NoStartFlow) GetPersistenceSchema() dex.PersistenceSchema { return dex.PersistenceSchema{} }

func (*ConditionalInputFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}
