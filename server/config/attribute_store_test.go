// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAttributeStoreConfigDecodeAndDefaults(t *testing.T) {
	path := writeTestConfig(t, `
blobStore:
  enabled: false
attributeStore:
  stores:
    reporting:
      type: postgres
      dsn: postgres://localhost/reporting
      tableName: public.flow_attributes
  schemaSyncInterval: 2m
  syncBatchSize: 25
  syncAttemptTimeout: 45s
  syncRetryPolicy:
    initialIntervalSeconds: 2
    maximumIntervalSeconds: 20
    backoffCoefficient: 1.5
    maximumAttempts: 4
    totalDurationSeconds: 90
`)
	cfg, err := NewConfig(path)
	require.NoError(t, err)
	require.Equal(t, 2*time.Minute, cfg.AttributeStore.EffectiveSchemaSyncInterval())
	require.Equal(t, 25, cfg.AttributeStore.EffectiveSyncBatchSize())
	require.Equal(t, 45*time.Second, cfg.AttributeStore.EffectiveSyncAttemptTimeout())
	policy := cfg.AttributeStore.EffectiveSyncRetryPolicy()
	require.Equal(t, int32(2), policy.GetInitialIntervalSeconds())
	require.Equal(t, int32(20), policy.GetMaximumIntervalSeconds())
	require.Equal(t, float32(1.5), policy.GetBackoffCoefficient())
	require.Equal(t, int32(4), policy.GetMaximumAttempts())
	require.Equal(t, int32(90), policy.GetTotalDurationSeconds())
	require.NoError(t, cfg.AttributeStore.Validate())
}

func TestExternalStorageConfigKeyIsRejected(t *testing.T) {
	path := writeTestConfig(t, "externalStorage:\n  enabled: true\n")
	_, err := NewConfig(path)
	require.ErrorContains(t, err, "externalStorage")
}

func writeTestConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}
