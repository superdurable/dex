// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package loaded

import "github.com/superdurable/dex/sdk-go/dex"

func wholeMapOptions(attributeMap dex.AttributeDef) *dex.StepOptions {
	options := &dex.StepOptions{}
	options.ExecuteLoadAttributeMaps = []dex.AttributeDef{attributeMap}
	return options
}

func recordRelease(ctx dex.Context, releaseIDs dex.AttributeMap[string], releaseID string) error {
	return releaseIDs.Set(ctx, releaseID, releaseID)
}
