// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dev

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	// defaultAgentMediaStorePort is the loopback port of the agent media store when --agent-media-store-port is not set.
	defaultAgentMediaStorePort = 8803
	// agentMediaStoreHost is the only address the agent media store binds, whatever --bind-address is.
	agentMediaStoreHost = "127.0.0.1"
	// agentMediaStoreBucket is the S3 bucket that dex-server-zd serves for applications.
	agentMediaStoreBucket = "dex-dev-media"
	// agentMediaStorePrefix is the key prefix of agent media inside agentMediaStoreBucket.
	agentMediaStorePrefix = "agents"
	// zdApplicationS3ListenFlag is the dex-server-zd dev flag that serves the application bucket.
	zdApplicationS3ListenFlag = "--application-s3-listen"
	// zdApplicationS3BucketFlag names the bucket that zdApplicationS3ListenFlag serves.
	zdApplicationS3BucketFlag = "--application-s3-bucket"
	zdHelpProbeTimeout        = 10 * time.Second
)

// agentMediaStoreURI returns the S3 URI of the agent media store, as clients configure it.
func agentMediaStoreURI() string {
	return "s3://" + agentMediaStoreBucket + "/" + agentMediaStorePrefix
}

// agentMediaStoreReadyLine returns the ready output line that announces the agent media store at endpoint.
func agentMediaStoreReadyLine(endpoint string) string {
	return fmt.Sprintf("Agent media store: %s endpoint=%s", agentMediaStoreURI(), endpoint)
}

// zdServesApplicationS3 reports whether the dev help of zdBinary lists --application-s3-listen.
// It returns an error when the help command cannot run.
func zdServesApplicationS3(ctx context.Context, zdBinary string) (bool, error) {
	probeCtx, cancel := context.WithTimeout(ctx, zdHelpProbeTimeout)
	defer cancel()
	output, err := exec.CommandContext(probeCtx, zdBinary, "dev", "--help").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("run %s dev --help: %w", zdBinary, err)
	}
	return strings.Contains(string(output), zdApplicationS3ListenFlag), nil
}
