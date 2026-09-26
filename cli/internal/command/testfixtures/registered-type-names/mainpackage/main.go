// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package main

import (
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

// A go test build compiles package main under its import path, so only a real binary shows runtime names.
func main() {
	for _, registered := range []struct {
		nodeID string
		name   string
	}{
		{nodeID: "flow", name: dex.GetFinalFlowType(&OrderFlow{})},
		{nodeID: "step:awaitOrder", name: dex.GetFinalStepType[orderInput](awaitOrder{})},
		{nodeID: "step:shipOrder", name: dex.GetFinalStepType[orderInput](shipOrder[orderInput]{})},
	} {
		fmt.Printf("DEX_REGISTERED_TYPE_NAME %s %s\n", registered.nodeID, registered.name)
	}
}
