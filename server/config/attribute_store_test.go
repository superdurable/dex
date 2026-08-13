// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

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
    initialInterval: 250ms
    maximumInterval: 20s
    backoffCoefficient: 1.5
    maximumAttempts: 4
    totalDuration: 1m30s
`)
	cfg, err := NewConfig(path)
	require.NoError(t, err)
	require.False(t, cfg.BlobStore.EffectiveEnabled())
	require.Equal(t, DefaultBlobStoreThresholdInBytes, cfg.BlobStore.EffectiveThresholdInBytes())
	require.Equal(t, 2*time.Minute, cfg.AttributeStore.EffectiveSchemaSyncInterval())
	require.Equal(t, 25, cfg.AttributeStore.EffectiveSyncBatchSize())
	require.Equal(t, 45*time.Second, cfg.AttributeStore.EffectiveSyncAttemptTimeout())
	policy := cfg.AttributeStore.EffectiveSyncRetryPolicy()
	require.Equal(t, 250*time.Millisecond, policy.InitialInterval)
	require.Equal(t, 20*time.Second, policy.MaximumInterval)
	require.Equal(t, 1.5, policy.BackoffCoefficient)
	require.Equal(t, int32(4), policy.MaximumAttempts)
	require.Equal(t, 90*time.Second, policy.TotalDuration)
	require.NoError(t, cfg.AttributeStore.Validate())
}

func TestBlobStoreDefaults(t *testing.T) {
	path := writeTestConfig(t, "{}\n")
	cfg, err := NewConfig(path)
	require.NoError(t, err)
	require.True(t, cfg.BlobStore.EffectiveEnabled())
	require.Equal(t, 1024, cfg.BlobStore.EffectiveThresholdInBytes())
	require.Equal(t, 100*time.Millisecond, cfg.AttributeStore.EffectiveSyncRetryPolicy().InitialInterval)
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
