// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build !unix

package connectorsmcp

import "errors"

type exclusiveFileLock struct{}

// lockFile fails: dexcli releases only Unix builds, and keyed writes need a cross-process lock.
func lockFile(string) (*exclusiveFileLock, error) {
	return nil, errors.New("connector call state locks require a Unix system")
}

func (*exclusiveFileLock) unlock() error { return nil }
