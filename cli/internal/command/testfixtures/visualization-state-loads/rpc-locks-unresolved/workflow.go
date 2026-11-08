// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package rpclocksunresolved

import "github.com/superdurable/dex/sdk-go/dex"

var (
	Status = dex.DefineAttribute[string]("case-status")
	Gate   = dex.DefineAttribute[string]("gate-key")
)

type UnresolvedRPCLocksFlow struct{ dex.FlowDefaults }

func (*UnresolvedRPCLocksFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(HoldStep{})}
}

func (flow *UnresolvedRPCLocksFlow) GetRPCs() []dex.RPCDef {
	statusLock := dex.LockAttribute(Status)
	return []dex.RPCDef{
		dex.DefineRPC(flow.ApproveCase, &dex.RPCOptions{
			Name:           "approve_case",
			LockAttributes: []dex.AttributeLock{statusLock, dex.LockAttribute(Gate), dex.LockAttribute(Status)},
		}),
		dex.DefineRPC(flow.AssignOwner, &dex.RPCOptions{
			LockAttributes: locksOf(Status, Gate),
			Action:         dex.DefineAction("Assign owner", dex.WhenAttributeMatches(Status, dex.AttributeMatchEqual("open")), dex.ActionRequiresPermission("case.assign")),
		}),
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (*UnresolvedRPCLocksFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{Status, Gate}}
}

func (*UnresolvedRPCLocksFlow) ApproveCase(ctx dex.Context, _ dex.None) (*dex.RPCResult[bool], error) {
	status, err := Status.Get(ctx)
	if err != nil || status != "open" {
		return &dex.RPCResult[bool]{Output: false}, err
	}
	if err := Status.Set(ctx, "approved"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[bool]{Output: true}, nil
}

func (*UnresolvedRPCLocksFlow) AssignOwner(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := Gate.Set(ctx, "assigned"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*UnresolvedRPCLocksFlow) GetDexSummary(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*UnresolvedRPCLocksFlow) GetDexDisplay(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
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

func locksOf(attributes ...dex.Attribute[string]) []dex.AttributeLock {
	locks := make([]dex.AttributeLock, 0, len(attributes))
	for _, attribute := range attributes {
		locks = append(locks, dex.LockAttribute(attribute))
	}
	return locks
}
