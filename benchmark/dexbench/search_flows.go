// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import "github.com/superdurable/dex/sdk-go/dex"

type searchCommonFlow struct{ dex.FlowDefaults }
type searchSecondaryFlow struct{ searchCommonFlow }
type searchRareFlow struct{ searchCommonFlow }

func (searchCommonFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(searchCompletionStep{}), dex.DefineStep(searchHoldStep{})}
}

func (searchCommonFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{benchmarkWakeChannel}}
}

type searchCompletionStep struct {
	dex.StepDefaultsNoWaitFor[bool]
}

func (searchCompletionStep) Execute(_ dex.Context, shouldRemainRunning bool) (*dex.StepDecision, error) {
	if shouldRemainRunning {
		return dex.GoTo(searchHoldStep{}, nil), nil
	}
	return dex.GracefulComplete(nil), nil
}

type searchHoldStep struct{ dex.StepDefaults }

func (searchHoldStep) WaitFor(_ dex.Context, _ dex.None) (*dex.Wait, error) {
	return dex.Until(benchmarkWakeChannel.ForOne()), nil
}

func (searchHoldStep) Execute(_ dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}
