// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package waitfailure

import (
	"errors"

	"github.com/superdurable/dex/sdk-go/dex"
)

type WaitFailureFlow struct {
	dex.FlowDefaults
}

func (*WaitFailureFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(rejectOrder{}),
	}
}

func (flow *WaitFailureFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (*WaitFailureFlow) GetDexSummary(dex.Context, dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*WaitFailureFlow) GetDexDisplay(dex.Context, dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

// dex:group group-id:"orders" group-label:"Orders"
// dex:explanation text:"Rejects the order because orders are closed."
type rejectOrder struct {
	dex.StepDefaults
}

// WaitFor only fails, so the analyzer records a failure wait without conditions.
func (rejectOrder) WaitFor(_ dex.Context, _ string) (*dex.Wait, error) {
	return nil, errors.New("orders are closed")
}

func (rejectOrder) Execute(_ dex.Context, _ string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}
