// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"strings"
	"testing"
)

var temporalOnlyScenarioTests = map[string]struct{}{
	"TestAnyCommandCombinationFlowCadence":                      {},
	"TestAnyCommandCombinationFlowCadenceContinueAsNew":         {},
	"TestAnyCommandCombinationFlowTemporalContinueAsNew":        {},
	"TestAsyncRetryBudgetCadence":                               {},
	"TestAsyncRetryBudgetTemporal":                              {},
	"TestAttributeIndexSyncCadence":                             {},
	"TestChannelMultivalueCadence":                              {},
	"TestCreateFlowCadence":                                     {},
	"TestCreateFlowCadenceContinueAsNew":                        {},
	"TestDeadEndFlowCadence":                                    {},
	"TestDeadEndFlowCadenceContinueAsNew":                       {},
	"TestDeadEndFlowTemporalContinueAsNew":                      {},
	"TestFlowIDWithTenantPrefixPassesThroughTemporal":           {},
	"TestFlowTimeoutCadence":                                    {},
	"TestGreedyTimerFlowBaseCadence":                            {},
	"TestGreedyTimerFlowBaseCadenceContinueAsNew":               {},
	"TestGreedyTimerFlowBaseTemporalContinueAsNew":              {},
	"TestGreedyTimerFlowCadence":                                {},
	"TestGreedyTimerFlowCadenceContinueAsNew":                   {},
	"TestGreedyTimerFlowTemporalContinueAsNew":                  {},
	"TestHardDeleteAttributeBlobCacheTemporal":                  {},
	"TestHardDeleteAttributeCleanupTemporal":                    {},
	"TestHardDeleteAttributeStepWriteRequiresBlobStoreTemporal": {},
	"TestHardDeleteAttributeSubFlowCadence":                     {},
	"TestHardDeleteAttributesCadence":                           {},
	"TestLargeDataAttributesTemporalContinueAsNew":              {},
	"TestLockingWorkflowCadence":                                {},
	"TestLockingWorkflowCadenceContinueAsNew":                   {},
	"TestLockingWorkflowTemporalContinueAsNew":                  {},
	"TestPullParallelLastWriteCadence":                          {},
	"TestPullRPCDispatchRespectsCallerDeadlineCadence":          {},
	"TestPullSubFlowStartsWithoutStickyQueueCadence":            {},
	"TestPullUnknownNameRedispatchCadence":                      {},
	"TestPullWorkerDispatchCadence":                             {},
	"TestPullWorkerQueueSurvivesContinueAsNewCadence":           {},
	"TestPullWorkerQueueSurvivesContinueAsNewTemporal":          {},
	"TestPullWorkerStickyCapacityRecoveryCadence":               {},
	"TestPullWorkerStickyFallbackCadence":                       {},
	"TestRpcExternalStorageNonLockingCadence":                   {},
	"TestRpcExternalStorageSynchronousUpdateTemporal":           {},
	"TestRpcInlineTransportCadence":                             {},
	"TestRpcInlineTransportTemporal":                            {},
	"TestRpcReadBlobStoreUnavailableCadence":                    {},
	"TestRpcReadBlobStoreUnavailableTemporal":                   {},
	"TestRpcSignalWireCompatibilityCadence":                     {},
	"TestRpcSignalWireCompatibilityTemporal":                    {},
	"TestRpcTransactionalRequestTooLargeTemporal":               {},
	"TestRpcWorkerRequestTooLargeCadence":                       {},
	"TestRpcWorkerRequestTooLargeTemporal":                      {},
	"TestS3CleanupCadence":                                      {},
	"TestS3CleanupTemporal":                                     {},
	"TestS3GetSetDataAttributesCadence":                         {},
	"TestS3KeyPrefixLayoutAndCleanupTemporal":                   {},
	"TestS3WorkflowInitDataAttributesCadence":                   {},
	"TestS3WorkflowStartInputCadence":                           {},
	"TestS3WorkflowStateInputOptimizationCadence":               {},
	"TestS3WorkflowUpsertDataObjectsCadence":                    {},
	"TestSignalWorkflowCadence":                                 {},
	"TestSignalWorkflowCadenceContinueAsNew":                    {},
	"TestSignalWorkflowTemporalContinueAsNew":                   {},
	"TestStartFlowNoOptionsCadence":                             {},
	"TestStepLineageCadence":                                    {},
	"TestSubFlowConditionCadence":                               {},
	"TestTemporalMetrics":                                       {},
	"TestTimerFlowCadence":                                      {},
	"TestTimerFlowCadenceContinueAsNew":                         {},
	"TestTimerFlowTemporalContinueAsNew":                        {},
	"TestUpdateWorkerTargetCadence":                             {},
	"TestUpdateWorkerTargetTemporal":                            {},
	"TestWaitUntilSearchAttributesOptimizationWorkflowTemporal": {},
	"TestWebAPICadence":                                         {},
	"TestWebAPITemporal":                                        {},
}

func requireTemporalOnlyScenario(t *testing.T) {
	t.Helper()
	testName, _, _ := strings.Cut(t.Name(), "/")
	requireTemporalOnlyScenarioName(t, testName)
}

func requireTemporalOnlyScenarioName(t *testing.T, testName string) {
	t.Helper()
	if *dexServerBinary == "" {
		return
	}
	if _, isTemporalOnly := temporalOnlyScenarioTests[testName]; isTemporalOnly {
		t.Skip("Temporal-only assertion")
	}
}
