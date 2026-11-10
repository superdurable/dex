// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build darwin || linux

package web

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockFileExclusively polls a non-blocking flock(2), which unlike a blocking one observes ctx, until it holds the lock.
func lockFileExclusively(ctx context.Context, path string) (*os.File, error) {
	// O_NOFOLLOW refuses a planted link, and O_NONBLOCK keeps a planted FIFO from blocking the open.
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Connector connections lock file: %w", err)
	}
	if err := requirePrivateLockFile(file); err != nil {
		return nil, errors.Join(fmt.Errorf("Connector connections lock file %w", err), file.Close())
	}
	delay := connectorConnectionsFileLockFirstPollDelay
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return nil, errors.Join(fmt.Errorf("lock Connector connections lock file: %w", err), file.Close())
		}
		if err := waitForConnectorConnectionsFileLockPoll(ctx, delay); err != nil {
			return nil, errors.Join(err, file.Close())
		}
		delay = nextConnectorConnectionsFileLockPollDelay(delay)
	}
}

// requirePrivateLockFile accepts only a regular lock file of this user, removing any group and other access.
func requirePrivateLockFile(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("cannot be inspected: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("is not a regular file")
	}
	status, hasStatus := info.Sys().(*syscall.Stat_t)
	if !hasStatus || int(status.Uid) != os.Geteuid() {
		return errors.New("is not owned by the current user")
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := file.Chmod(0o600); err != nil {
			return fmt.Errorf("permissions cannot be restricted to 0600: %w", err)
		}
	}
	return nil
}
