// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package rpc

import "github.com/superdurable/dex/service"

// StateSelection identifies collection values loaded for one handler.
type StateSelection = service.StateSelection

// ValidateAndSortSelections validates and sorts collection names and instances.
func ValidateAndSortSelections(
	attributeMapInstances []string,
	channelNames []string,
	channelMapInstances []string,
) (StateSelection, error) {
	return service.ValidateAndSortStateSelections(
		attributeMapInstances,
		channelNames,
		channelMapInstances,
	)
}
