// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build darwin

package agentsterminal

import (
	"os"

	"golang.org/x/sys/unix"
)

// processGroupHasJobControl reads the kernel's job-control count of this
// process group; zero means an orphaned group.
func processGroupHasJobControl() bool {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", os.Getpid())
	if err != nil {
		debugLog("read the process group", err)
		return false
	}
	return process.Eproc.Jobc > 0
}
