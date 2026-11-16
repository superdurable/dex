// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build !darwin && !linux

package web

import (
	"context"
	"os"
)

// lockFileExclusively takes no file lock here, as the connectors Go SDK takes none on this platform either.
func lockFileExclusively(context.Context, string) (*os.File, error) {
	return nil, nil
}
