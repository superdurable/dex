// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWebEnvironmentOverridesYAML(t *testing.T) {
	t.Setenv("DEX_WEB_FLOW_RENDERING_SOURCE", "blobstore")
	t.Setenv("DEX_WEB_FLOW_RENDERING_STORAGE_ID", "p0")
	t.Setenv("DEX_WEB_FLOW_RENDERING_PREFIX", "_superverse/dex-web/flow-definitions")
	t.Setenv("DEX_WEB_WORK_QUEUE_PERMISSION_MODE", "trusted-header")
	t.Setenv("DEX_WEB_START_FLOW_WORKER_TARGET_HEADLESS", "true")
	t.Setenv("DEX_WEB_TRUST_FORWARDED_EMBEDDING_HEADERS", "true")
	path := writeTestConfig(t, `
web:
  flowRenderingSource: local
  workQueuePermissionMode: local-selector
`)
	cfg, err := NewConfig(path)
	require.NoError(t, err)
	require.Equal(t, "blobstore", cfg.Web.FlowRenderingSource)
	require.Equal(t, "p0", cfg.Web.FlowRenderingBlobStore.StorageID)
	require.Equal(t, "_superverse/dex-web/flow-definitions", cfg.Web.FlowRenderingBlobStore.Prefix)
	require.Equal(t, "trusted-header", cfg.Web.WorkQueuePermissionMode)
	require.True(t, cfg.Web.IsStartFlowWorkerTargetHeadless)
	require.True(t, cfg.Web.TrustForwardedEmbeddingHeaders)
}

func TestWebStartFlowWorkerTargetHeadlessDefaultsFalseAndReadsYAML(t *testing.T) {
	defaultConfig, err := NewConfig(writeTestConfig(t, "web: {}\n"))
	require.NoError(t, err)
	require.False(t, defaultConfig.Web.IsStartFlowWorkerTargetHeadless)

	yamlConfig, err := NewConfig(writeTestConfig(t, "web:\n  startFlowWorkerTargetHeadless: true\n"))
	require.NoError(t, err)
	require.True(t, yamlConfig.Web.IsStartFlowWorkerTargetHeadless)
}

func TestWebEnvironmentRejectsInvalidStartFlowHeadlessRouting(t *testing.T) {
	t.Setenv("DEX_WEB_START_FLOW_WORKER_TARGET_HEADLESS", "sometimes")
	_, err := NewConfig(writeTestConfig(t, "web: {}\n"))
	require.ErrorContains(t, err, "DEX_WEB_START_FLOW_WORKER_TARGET_HEADLESS must be a boolean")
}

func TestWebEnvironmentRejectsInvalidForwardedEmbeddingTrust(t *testing.T) {
	t.Setenv("DEX_WEB_TRUST_FORWARDED_EMBEDDING_HEADERS", "sometimes")
	_, err := NewConfig(writeTestConfig(t, "web: {}\n"))
	require.ErrorContains(t, err, "DEX_WEB_TRUST_FORWARDED_EMBEDDING_HEADERS must be a boolean")
}

func TestWebConfigRejectsMixedDefinitionSources(t *testing.T) {
	path := writeTestConfig(t, `
web:
  flowRenderingSource: blobstore
  flowRenderingDirectory: /tmp/definitions
  flowRenderingBlobStore:
    storageId: p0
    prefix: definitions
`)
	_, err := NewConfig(path)
	require.ErrorContains(t, err, "mutually exclusive")
}

func TestWebConfigAcceptsTrustedHeaderPermissionMode(t *testing.T) {
	path := writeTestConfig(t, `
web:
  workQueuePermissionMode: trusted-header
  trustForwardedEmbeddingHeaders: true
`)
	cfg, err := NewConfig(path)
	require.NoError(t, err)
	require.True(t, cfg.Web.TrustForwardedEmbeddingHeaders)
}

func TestWebConfigRejectsLegacyDefinitionSources(t *testing.T) {
	for _, source := range []string{"directory", "s3"} {
		t.Run(source, func(t *testing.T) {
			path := writeTestConfig(t, "web:\n  flowRenderingSource: "+source+"\n")
			_, err := NewConfig(path)
			require.ErrorContains(t, err, "must be local or blobstore")
		})
	}
}

func TestConfigRejectsReservedSuperVerseWorkflowNamespace(t *testing.T) {
	path := writeTestConfig(t, `
interpreter:
  temporal:
    namespace: _superverse
`)
	_, err := NewConfig(path)
	require.ErrorContains(t, err, "reserved")
}

func TestTemporalCloudOpsConfig(t *testing.T) {
	path := writeTestConfig(t, `
interpreter:
  temporal:
    namespace: test.account
    cloudAPIKey: secret
    cloudOps:
      hostPort: saas-api.tmprl.cloud:443
      apiVersion: v0.19.1
`)
	cfg, err := NewConfig(path)
	require.NoError(t, err)
	require.Equal(t, "saas-api.tmprl.cloud:443", cfg.Interpreter.Temporal.CloudOps.HostPort)
	require.Equal(t, "v0.19.1", cfg.Interpreter.Temporal.CloudOps.APIVersion)
}

func TestTemporalCloudOpsConfigValidation(t *testing.T) {
	testCases := []struct {
		name          string
		temporalYAML  string
		errorContains string
	}{
		{
			name: "API key required",
			temporalYAML: `
    cloudOps:
      hostPort: saas-api.tmprl.cloud:443
      apiVersion: v0.19.1`,
			errorContains: "cloudAPIKey is required",
		},
		{
			name: "host and port required",
			temporalYAML: `
    cloudAPIKey: secret
    cloudOps:
      hostPort: saas-api.tmprl.cloud
      apiVersion: v0.19.1`,
			errorContains: "hostPort must be host:port",
		},
		{
			name: "version required",
			temporalYAML: `
    cloudAPIKey: secret
    cloudOps:
      hostPort: saas-api.tmprl.cloud:443`,
			errorContains: "apiVersion must be a version",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			path := writeTestConfig(t, "interpreter:\n  temporal:"+testCase.temporalYAML+"\n")
			_, err := NewConfig(path)
			require.ErrorContains(t, err, testCase.errorContains)
		})
	}
}

func TestConfigAcceptsExternallyManagedIndexesOption(t *testing.T) {
	path := writeTestConfig(t, `
interpreter:
  attributeIndexesManagedExternally: true
`)
	cfg, err := NewConfig(path)
	require.NoError(t, err)
	require.True(t, cfg.Interpreter.AttributeIndexesManagedExternally)
}

func TestRetryPolicyConfigUsesDurations(t *testing.T) {
	path := writeTestConfig(t, `
api:
  queryWorkflowFailedRetryPolicy:
    initialInterval: 125ms
    backoffCoefficient: 1.25
    maximumInterval: 750ms
    maximumAttempts: 7
    totalDuration: 3s
  invokeRPCContinuedAsNewErrorRetryPolicy:
    initialInterval: 100ms
    backoffCoefficient: 2
    maximumInterval: 1s
    totalDuration: 5s
interpreter:
  interpreterActivityConfig:
    dumpWorkflowInternalActivityConfig:
      retryPolicy:
        initialInterval: 250ms
        backoffCoefficient: 1.5
        maximumInterval: 2s
        maximumAttempts: 4
        totalDuration: 10s
`)
	cfg, err := NewConfig(path)
	require.NoError(t, err)
	require.Equal(t, 125*time.Millisecond, cfg.Api.QueryWorkflowFailedRetryPolicy.InitialInterval)
	require.Equal(t, 750*time.Millisecond, cfg.Api.QueryWorkflowFailedRetryPolicy.MaximumInterval)
	require.Equal(t, 100*time.Millisecond, cfg.Api.InvokeRPCContinuedAsNewErrorRetryPolicy.InitialInterval)
	require.Equal(t, 5*time.Second, cfg.Api.InvokeRPCContinuedAsNewErrorRetryPolicy.TotalDuration)
	activityPolicy := cfg.Interpreter.InterpreterActivityConfig.DumpWorkflowInternalActivityConfig.RetryPolicy
	require.Equal(t, 250*time.Millisecond, activityPolicy.InitialInterval)
	require.Equal(t, 10*time.Second, activityPolicy.TotalDuration)
}

func TestRetryPolicyConfigRejectsInvalidDuration(t *testing.T) {
	path := writeTestConfig(t, `
api:
  invokeRPCContinuedAsNewErrorRetryPolicy:
    initialInterval: 2s
    maximumInterval: 100ms
`)
	_, err := NewConfig(path)
	require.ErrorContains(t, err, "maximumInterval must not be less than initialInterval")
}

func TestMinimumStepHeartbeatTimeoutConfig(t *testing.T) {
	require.Equal(t, 10*time.Second,
		(InterpreterActivityConfig{}).EffectiveMinimumStepHeartbeatTimeout())

	path := writeTestConfig(t, `
interpreter:
  interpreterActivityConfig:
    minimumStepHeartbeatTimeout: 2s
`)
	cfg, err := NewConfig(path)
	require.NoError(t, err)
	require.Equal(t, 2*time.Second,
		cfg.Interpreter.InterpreterActivityConfig.EffectiveMinimumStepHeartbeatTimeout())

	path = writeTestConfig(t, `
interpreter:
  interpreterActivityConfig:
    minimumStepHeartbeatTimeout: -1s
`)
	_, err = NewConfig(path)
	require.ErrorContains(t, err, "minimumStepHeartbeatTimeout must be non-negative")
}

func TestCleanupStrategyCronSchedule(t *testing.T) {
	testCases := []struct {
		name             string
		strategy         CleanupStrategy
		expectedSchedule string
		expectError      bool
	}{
		{
			name: "default strategy disabled",
		},
		{
			name: "default strategy daily",
			strategy: CleanupStrategy{
				CleanupFrequencyInDays: 1,
			},
			expectedSchedule: "0 0 * * *",
		},
		{
			name: "explicit strategy every three days",
			strategy: CleanupStrategy{
				CleanupStrategyType:    CleanupStrategyTypeAfterAllRunsDeleted,
				CleanupFrequencyInDays: 3,
			},
			expectedSchedule: "0 0 */3 * *",
		},
		{
			name: "unsupported strategy",
			strategy: CleanupStrategy{
				CleanupStrategyType: "unsupported",
			},
			expectError: true,
		},
		{
			name: "negative frequency",
			strategy: CleanupStrategy{
				CleanupFrequencyInDays: -1,
			},
			expectError: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			schedule, err := testCase.strategy.CronSchedule()
			if testCase.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, testCase.expectedSchedule, schedule)
		})
	}
}
