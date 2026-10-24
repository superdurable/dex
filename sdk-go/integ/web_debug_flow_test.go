// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import "github.com/superdurable/dex/sdk-go/dex"

var (
	webDebugRelease = dex.DefineChannel[string]("web-debug-release")
	// Nothing waits on web-debug-notes, so its messages stay pending until deleted.
	webDebugNotes = dex.DefineChannel[string]("web-debug-notes")
)

// webDebugChannelFlow gives the Dex Web debugging tests a run with pending Channel messages.
type webDebugChannelFlow struct {
	dex.FlowDefaults
}

func (webDebugChannelFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(webDebugAwaitReleaseStep{})}
}

func (webDebugChannelFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{webDebugRelease, webDebugNotes}}
}

type webDebugAwaitReleaseStep struct {
	dex.StepDefaults
}

func (webDebugAwaitReleaseStep) WaitFor(dex.Context, struct{}) (*dex.Wait, error) {
	return dex.AllOf(webDebugRelease.ForOne()), nil
}

func (webDebugAwaitReleaseStep) Execute(dex.Context, struct{}) (*dex.StepDecision, error) {
	return dex.GracefulComplete("released"), nil
}
