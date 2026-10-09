// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package unloaded

import "github.com/superdurable/dex/sdk-go/dex"

func deliveryAddress(ctx dex.Context, subscribers dex.AttributeMap[string], recipient string) (string, error) {
	return subscribers.Get(ctx, recipient)
}
