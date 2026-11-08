// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package rpclocks

import "github.com/superdurable/dex/sdk-go/dex"

var (
	Status = dex.DefineAttribute[string]("case-status")
	Gate   = dex.DefineAttribute[string]("gate-key")
	Owners = dex.DefineAttributeMap[string]("owners")
)

type RPCLocksFlow struct{ dex.FlowDefaults }

func (*RPCLocksFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(HoldStep{})}
}

func (flow *RPCLocksFlow) GetRPCs() []dex.RPCDef {
	statusLock := dex.LockAttribute(Status)
	return []dex.RPCDef{
		dex.DefineRPC(flow.ApproveCase, &dex.RPCOptions{
			Name:           "approve_case",
			LockAttributes: []dex.AttributeLock{statusLock, dex.LockAttribute(Gate), dex.LockAttribute(Status)},
		}),
		dex.DefineRPC(flow.AssignOwner, &dex.RPCOptions{
			LockAttributes: []dex.AttributeLock{dex.LockAttributeMap(Owners, "primary")},
		}),
		dex.DefineRPC(flow.GetDexSummary, nil),
	}
}

func (*RPCLocksFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{Status, Gate, Owners}}
}

func (*RPCLocksFlow) ApproveCase(ctx dex.Context, _ dex.None) (*dex.RPCResult[bool], error) {
	status, err := Status.Get(ctx)
	if err != nil || status != "open" {
		return &dex.RPCResult[bool]{Output: false}, err
	}
	if err := Status.Set(ctx, "approved"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[bool]{Output: true}, nil
}

func (*RPCLocksFlow) AssignOwner(ctx dex.Context, owner string) (*dex.RPCResult[dex.None], error) {
	if err := Owners.Set(ctx, "primary", owner); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*RPCLocksFlow) GetDexSummary(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

// dex:group group-id:hold group-label:"Hold"
// dex:explanation text:"Wait for the case to close."
type HoldStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (HoldStep) Execute(_ dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}
