// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package sharedoptions

import "github.com/superdurable/dex/sdk-go/dex"

func ExecuteWholeMaps(attributeMaps ...dex.AttributeDef) *dex.StepOptions {
	return &dex.StepOptions{ExecuteLoadAttributeMaps: attributeMaps}
}

func RPCWholeMaps(attributeMaps ...dex.AttributeDef) *dex.RPCOptions {
	return &dex.RPCOptions{LoadAttributeMaps: attributeMaps}
}
