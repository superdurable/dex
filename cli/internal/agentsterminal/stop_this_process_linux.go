// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build linux

package agentsterminal

import (
	"runtime"

	"golang.org/x/sys/unix"
)

// stopThisProcess sends SIGTSTP to the calling thread. Linux may deliver a
// process-wide kill to another thread after the handler is back, which then
// suspends the chat again.
func stopThisProcess() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// A thread-directed signal is delivered before the call returns.
	if err := unix.Tgkill(unix.Getpid(), unix.Gettid(), unix.SIGTSTP); err != nil {
		debugLog("stop this process", err)
	}
}
