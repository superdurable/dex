// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build unix

package connectorsmcp

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// exclusiveFileLock is an advisory lock that another dexcli process sharing the state directory respects.
type exclusiveFileLock struct {
	file *os.File
}

// lockFile blocks until it holds an exclusive lock on path, creating the file when it is missing.
func lockFile(path string) (*exclusiveFileLock, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		return nil, errors.Join(fmt.Errorf("lock %s: %w", path, err), file.Close())
	}
	return &exclusiveFileLock{file: file}, nil
}

// unlock releases the lock; closing the descriptor releases it even when the explicit unlock fails.
func (lock *exclusiveFileLock) unlock() error {
	unlockErr := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	return errors.Join(unlockErr, lock.file.Close())
}
