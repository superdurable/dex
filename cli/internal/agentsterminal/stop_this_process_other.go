// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build !linux

package agentsterminal

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

// stopSignalEchoWait is how long a stop waits for its own SIGTSTP to come
// back to a handler.
const stopSignalEchoWait = 100 * time.Millisecond

// stopThisProcess sends SIGTSTP to this process. A signal the kernel did not
// act on before kill returned is caught here, so it never suspends again.
func stopThisProcess() {
	if err := syscall.Kill(os.Getpid(), syscall.SIGTSTP); err != nil {
		debugLog("stop this process", err)
		return
	}
	echo := make(chan os.Signal, 1)
	signal.Notify(echo, syscall.SIGTSTP)
	defer signal.Stop(echo)
	timer := time.NewTimer(stopSignalEchoWait)
	defer timer.Stop()
	select {
	case <-echo:
	case <-timer.C:
	}
}
