// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorsmcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/superdurable/dex-connectors-library/sdkgo/projectconfig"
)

// hostedProjectEnvironment lists the variables that make LoadFromEnvironment refuse a local connections file.
var hostedProjectEnvironment = []string{"DEX_PROJECT_ID", "DEX_PROJECT_SCOPE", "DEX_PROJECT_CONFIG_KEY", "DEX_PROJECT_STORAGE_BUCKET"}

// localProjectEnvironmentMu serializes the process-wide variable that selects the local connections file.
var localProjectEnvironmentMu sync.Mutex

// loadLocalProject returns the project of the connections file Dex Web writes, or an error matching
// os.ErrNotExist when the file is missing.
func loadLocalProject(ctx context.Context, connectionsPath string) (*projectconfig.LoadedProject, error) {
	// The SDK then resolves, refreshes, and writes back credentials, as for a Dex application.
	for _, name := range hostedProjectEnvironment {
		if os.Getenv(name) != "" {
			return nil, fmt.Errorf("unset %s: it selects a hosted project instead of the local connections file", name)
		}
	}
	if _, err := os.Lstat(connectionsPath); err != nil {
		return nil, err
	}
	localProjectEnvironmentMu.Lock()
	defer localProjectEnvironmentMu.Unlock()
	previous, hadPrevious := os.LookupEnv(projectconfig.LocalConnectionsFileEnvironment)
	if err := os.Setenv(projectconfig.LocalConnectionsFileEnvironment, connectionsPath); err != nil {
		return nil, fmt.Errorf("select the local connections file: %w", err)
	}
	project, loadErr := projectconfig.LoadFromEnvironment(ctx)
	var restoreErr error
	if hadPrevious {
		restoreErr = os.Setenv(projectconfig.LocalConnectionsFileEnvironment, previous)
	} else {
		restoreErr = os.Unsetenv(projectconfig.LocalConnectionsFileEnvironment)
	}
	if restoreErr != nil {
		return nil, errors.Join(loadErr, fmt.Errorf("restore %s: %w", projectconfig.LocalConnectionsFileEnvironment, restoreErr))
	}
	if loadErr != nil {
		return nil, loadErr
	}
	return project, nil
}
