// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package defaults

import (
	"fmt"
	"testing"

	"github.com/superdurable/dex/cli/internal/command/testfixtures/registered-type-names/model"
	"github.com/superdurable/dex/cli/internal/command/testfixtures/registered-type-names/versioned.v2"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestPrintRegisteredTypeNames(t *testing.T) {
	for _, registered := range []struct {
		nodeID string
		name   string
	}{
		{nodeID: "flow", name: dex.GetFinalFlowType(&Flow{})},
		{nodeID: "step:awaitDecision", name: dex.GetFinalStepType[model.Input](awaitDecision{})},
		{nodeID: "step:pointerStep", name: dex.GetFinalStepType[model.Input](&pointerStep{})},
		{nodeID: "step:emptyOverrideStep", name: dex.GetFinalStepType[model.Input](emptyOverrideStep{})},
		{nodeID: "step:concatenatedStep", name: dex.GetFinalStepType[model.Input](concatenatedStep{})},
		{nodeID: "step:localConstantStep", name: dex.GetFinalStepType[model.Input](localConstantStep{})},
		{nodeID: "step:branchingOverrideStep", name: dex.GetFinalStepType[model.Input](branchingOverrideStep{})},
		{nodeID: "step:promotedOverrideStep", name: dex.GetFinalStepType[model.Input](promotedOverrideStep{})},
		{nodeID: "step:namesFileStep", name: dex.GetFinalStepType[model.Input](namesFileStep{})},
		{nodeID: "step:helperPromotedStep", name: dex.GetFinalStepType[model.Input](helperPromotedStep{})},
		{nodeID: "step:box", name: dex.GetFinalStepType[model.Input](box[model.Input]{})},
		{nodeID: "step:pair", name: dex.GetFinalStepType[model.Input](pair[int, []string]{})},
		{nodeID: "step:byteBox", name: dex.GetFinalStepType[model.Input](byteBox[byte]{})},
		{nodeID: "step:anyBox", name: dex.GetFinalStepType[model.Input](anyBox[any]{})},
		{nodeID: "step:mapBox", name: dex.GetFinalStepType[model.Input](mapBox[map[string]*model.Input]{})},
		{nodeID: "step:nested", name: dex.GetFinalStepType[model.Input](nested[box[int]]{})},
		{nodeID: "step:versionedBox", name: dex.GetFinalStepType[model.Input](versionedBox[versioned.Item]{})},
	} {
		fmt.Printf("DEX_REGISTERED_TYPE_NAME %s %s\n", registered.nodeID, registered.name)
	}
}
