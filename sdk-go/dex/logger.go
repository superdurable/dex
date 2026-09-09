// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import "github.com/superdurable/dex/sdk-go/logging"

// Logger receives structured SDK logs.
type Logger = logging.Logger

func resolveLogger(override Logger, fallback Logger) Logger {
	if override != nil {
		return override
	}
	return logging.OrDefault(fallback)
}
