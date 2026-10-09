// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package appendoptions

import "github.com/superdurable/dex/sdk-go/dex"

var (
	First   = dex.DefineAttributeMap[string]("first")
	Second  = dex.DefineAttributeMap[string]("second")
	Third   = dex.DefineAttributeMap[string]("third")
	Missing = dex.DefineAttributeMap[string]("missing")
	Status  = dex.DefineAttribute[string]("status")
)

type AppendOptionsFlow struct{ dex.FlowDefaults }

func (*AppendOptionsFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(ReadMapsStep{})}
}

func (flow *AppendOptionsFlow) GetRPCs() []dex.RPCDef {
	locks := []dex.AttributeLock{dex.LockAttribute(Status)}
	locks = append(locks, dex.LockAttributeMap(First, "owner"))
	return []dex.RPCDef{
		dex.DefineRPC(flow.UpdateStatus, &dex.RPCOptions{
			LockAttributes: locks,
			Action:         dex.DefineAction("Update status", dex.WhenAttributeMatches(Status, dex.AttributeMatchEqual("ready")), dex.ActionRequiresPermission("status.update")),
		}),
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (*AppendOptionsFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{First, Second, Third, Missing, Status}}
}

func (*AppendOptionsFlow) UpdateStatus(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	previous, err := Status.Get(ctx)
	if err != nil {
		return nil, err
	}
	if previous == "updated" {
		return &dex.RPCResult[dex.None]{}, nil
	}
	if err := Status.Set(ctx, "updated"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*AppendOptionsFlow) GetDexSummary(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*AppendOptionsFlow) GetDexDisplay(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

// dex:group group-id:read group-label:"Read"
// dex:explanation text:"Read the loaded maps and detect a missing load."
type ReadMapsStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (ReadMapsStep) GetStepOptions() *dex.StepOptions {
	maps := []dex.AttributeDef{First}
	maps = append(maps, Second)
	maps = append(maps, appendMapLoads(Third)...)
	options := &dex.StepOptions{ExecuteLoadAttributeMaps: maps}
	alias := options
	options = alias
	return options
}

func (ReadMapsStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	count := First.MapSize(ctx) + Second.MapSize(ctx) + Third.MapSize(ctx) + Missing.MapSize(ctx)
	return dex.GracefulComplete(count), nil
}

func appendMapLoads(attributeMap dex.AttributeDef) []dex.AttributeDef {
	loads := []dex.AttributeDef{attributeMap}
	loads = append(loads, forwardMapLoads(loads)...)
	return loads
}

func forwardMapLoads(loads []dex.AttributeDef) []dex.AttributeDef { return loads }
