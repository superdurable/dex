// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

const (
	// connectorConnectionsFileLockSuffix names the lock file beside the connections file: connections.json.lock.
	connectorConnectionsFileLockSuffix = ".lock"
	// connectorConnectionsFileLockMaxWait covers an application holding the lock across a provider token refresh.
	connectorConnectionsFileLockMaxWait = 15 * time.Second
	// The non-blocking file lock is polled with a doubling delay between these bounds.
	connectorConnectionsFileLockFirstPollDelay = 5 * time.Millisecond
	connectorConnectionsFileLockMaxPollDelay   = 100 * time.Millisecond
)

var errConnectorConnectionsFileBusy = errors.New("Connector connections file is locked by another process")

// lockConnectorConnectionsFile takes the lock that the connectors Go SDK takes for credential write-back.
func lockConnectorConnectionsFile(ctx context.Context, lockPath string) (*os.File, error) {
	waitContext, cancel := context.WithTimeout(ctx, connectorConnectionsFileLockMaxWait)
	defer cancel()
	return lockFileExclusively(waitContext, lockPath)
}

// connectorConnectionsFileLockWaitObserverKey is the context key of a test callback run when a lock attempt waits.
type connectorConnectionsFileLockWaitObserverKey struct{}

// notifyConnectorConnectionsFileLockWait runs the wait observer that ctx carries, if any.
func notifyConnectorConnectionsFileLockWait(ctx context.Context) {
	if observer, hasObserver := ctx.Value(connectorConnectionsFileLockWaitObserverKey{}).(func()); hasObserver {
		observer()
	}
}

// waitForConnectorConnectionsFileLockPoll waits one polling delay unless ctx ends first.
func waitForConnectorConnectionsFileLockPoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("%w: wait for the lock: %w", errConnectorConnectionsFileBusy, ctx.Err())
	case <-timer.C:
		return nil
	}
}

func nextConnectorConnectionsFileLockPollDelay(delay time.Duration) time.Duration {
	return min(2*delay, connectorConnectionsFileLockMaxPollDelay)
}
