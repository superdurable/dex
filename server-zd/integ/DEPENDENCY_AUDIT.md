# Integration suite dependency audit

This inventory records every import into the original Go Server module and every
Temporal/Cadence import under `server/integ` before extraction. Import numbers
in the source table map to the complete paths below. Test entrypoints list all
tests declared in each consuming file; shared harness files serve every test
that calls startDexService/startWorker or their helpers. Workflow fixture
imports serve the tests that import that fixture package.

The new module uses sdk-go protocol types and pointer helpers. It replaces
reserved protocol strings, JSON logging, RFC3339Nano formatting, and fixture
configuration locally. It deletes bootstrap, API/interpreter setup, unified
clients, backend history/queries, Blob Store assertions, Go package tests,
matching/membership internals, and Temporal metrics/authentication setup.

## Imported packages

| Import | Original package | Extraction decision |
| --- | --- | --- |
| 1 | github.com/superdurable/dex/gen/dexpb | Use sdk-go/gen/dexpb. |
| 2 | github.com/superdurable/dex/service | Copy reserved protocol identifiers; remove backend dispatch and package version mutation. |
| 3 | github.com/superdurable/dex/service/common/converter | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 4 | github.com/superdurable/dex/service/common/ptr | Use sdk-go/dex/ptr. |
| 5 | go.temporal.io/api/common/v1 | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 6 | go.temporal.io/api/enums/v1 | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 7 | go.temporal.io/api/failure/v1 | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 8 | go.temporal.io/api/workflowservice/v1 | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 9 | go.temporal.io/sdk/converter | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 10 | go.uber.org/cadence/.gen/go/shared | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 11 | go.uber.org/cadence/client | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 12 | go.uber.org/cadence/encoded | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 13 | github.com/superdurable/dex/config | Keep only harness-written config shapes in internal/testconfig. |
| 14 | github.com/superdurable/dex/service/client | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 15 | go.temporal.io/sdk/client | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 16 | github.com/superdurable/dex/service/client/temporal | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 17 | go.temporal.io/api/history/v1 | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 18 | github.com/superdurable/dex/service/auth | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 19 | github.com/superdurable/dex/service/common/log/loggerimpl | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 20 | github.com/superdurable/dex/service/bootstrap | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 21 | go.uber.org/cadence/.gen/go/cadence/workflowserviceclient | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 22 | go.temporal.io/sdk/contrib/tally | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 23 | github.com/superdurable/dex/service/common/timeparser | Use time.RFC3339Nano. |
| 24 | github.com/superdurable/dex/service/api | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 25 | github.com/superdurable/dex/service/client/cadence | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 26 | github.com/superdurable/dex/service/common/attributestore | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 27 | github.com/superdurable/dex/service/common/blobstore | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 28 | github.com/superdurable/dex/service/common/log | Use standard JSON logging in fixtures. |
| 29 | github.com/superdurable/dex/service/common/pulldispatch | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 30 | github.com/superdurable/dex/service/common/streamstore | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 31 | github.com/superdurable/dex/service/common/workerclient | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 32 | github.com/superdurable/dex/service/indexsync | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 33 | github.com/superdurable/dex/service/interpreter | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 34 | github.com/superdurable/dex/service/interpreter/cadence | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 35 | github.com/superdurable/dex/service/interpreter/temporal | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 36 | github.com/superdurable/dex/service/matching | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 37 | github.com/superdurable/dex/service/membership | Remove backend-only setup/assertions or leave the Go Server tests behind. |
| 38 | github.com/superdurable/dex/service/common/event | Remove backend-only setup/assertions or leave the Go Server tests behind. |

## Original consumers and tests

| Source under server/integ | Imports | Declared tests / consumers |
| --- | --- | --- |
| [any_command_close_test.go](../../server/integ/any_command_close_test.go) | 1, 2 | TestAnyCommandCloseFlowTemporal, TestAnyCommandCloseFlowTemporalContinueAsNew, TestAnyCommandCloseFlowCadence, TestAnyCommandCloseFlowCadenceContinueAsNew |
| [any_command_combination_test.go](../../server/integ/any_command_combination_test.go) | 1, 2 | TestAnyCommandCombinationFlowTemporal, TestAnyCommandCombinationFlowTemporalContinueAsNew, TestAnyCommandCombinationFlowCadence, TestAnyCommandCombinationFlowCadenceContinueAsNew |
| [any_timer_signal_test.go](../../server/integ/any_timer_signal_test.go) | 1, 2 | TestAnyTimerSignalFlowTemporal, TestGreedyAnyTimerSignalFlowTemporal, TestAnyTimerSignalFlowCadence, TestGreedyAnyTimerSignalFlowCadence, TestAnyTimerSignalFlowTemporalContinueAsNew, TestGreedyAnyTimerSignalFlowTemporalContinueAsNew, TestAnyTimerSignalFlowCadenceContinueAsNew, TestGreedyAnyTimerSignalFlowCadenceContinueAsNew |
| [async_retry_budget_test.go](../../server/integ/async_retry_budget_test.go) | 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12 | TestAsyncRetryBudgetTemporal, TestAsyncRetryBudgetCadence |
| [attribute_index_sync_test.go](../../server/integ/attribute_index_sync_test.go) | 1, 2 | TestAttributeIndexSyncTemporal, TestAttributeIndexSyncCadence |
| [attribute_sync_integ_test.go](../../server/integ/attribute_sync_integ_test.go) | 13, 1, 2 | TestAttributeSyncTemporal, TestAttributeSyncCadence, TestMongoDBAttributeSyncTemporal, TestMongoDBAttributeSyncCadence, TestAttributeSyncFlowTimeoutTemporal, TestAttributeSyncFlowTimeoutCadence, TestAttributeSyncRetryExhaustionTemporal, TestAttributeSyncRetryExhaustionCadence, TestAttributeSyncInvokeRPCGracefulCompleteTemporal |
| [attribute_sync_map_table_integ_test.go](../../server/integ/attribute_sync_map_table_integ_test.go) | 13, 1, 2 | TestAttributeSyncMapTablesTemporal, TestAttributeSyncMapTablesCadence |
| [attribute_sync_row_key_integ_test.go](../../server/integ/attribute_sync_row_key_integ_test.go) | 13, 1, 2 | TestAttributeSyncRowKeysTemporal, TestAttributeSyncRowKeysCadence |
| [basic_test.go](../../server/integ/basic_test.go) | 1, 2, 4 | TestBasicFlowTemporal, TestBasicFlowCadence |
| [binary_dex.go](../../server/integ/binary_dex.go) | 13, 1, 2, 14, 4 | Shared integration harness / helper |
| [binary_dex_test.go](../../server/integ/binary_dex_test.go) | 13, 1, 2, 4 | TestBinaryDexLauncher, TestBinaryDexConfigUnsupportedSetting, TestBinaryDexConfigMapping, TestBinaryDexConfigAttributeStoreMapping, TestBinaryDexLauncherEarlyExit, TestBinaryTemporalOnlyScenarioSkipsBeforePolling, TestBinaryDexLauncherReadinessTimeout, TestBinaryDexLauncherKillsUnresponsiveProcess, TestBinaryDexLauncherDisablesStreamsByDefault |
| [channel_message_queue_test.go](../../server/integ/channel_message_queue_test.go) | 1, 2, 4 | TestChannelMessageQueueTemporal, TestChannelMessageQueueCadence |
| [channel_multivalue_test.go](../../server/integ/channel_multivalue_test.go) | 1, 2, 4 | TestChannelMultivalueTemporal, TestChannelMultivalueCadence |
| [command_thread_completion_test.go](../../server/integ/command_thread_completion_test.go) | 1, 2, 4 | TestCommandThreadCompletionTemporal, TestCommandThreadCompletionCadence, TestAnyCommandCompletedTemporal, TestAnyCommandCompletedCadence |
| [conditional_close_committed_consumption_test.go](../../server/integ/conditional_close_committed_consumption_test.go) | 1, 2 | TestForceCompleteIfChannelsEmptyCountsCommittedConsumptionTemporal, TestForceCompleteIfChannelsEmptyCountsCommittedConsumptionCadence |
| [conditional_close_test.go](../../server/integ/conditional_close_test.go) | 1, 2 | TestConditionalForceCompleteOnInternalChannelEmptyWorkflowTemporal, TestConditionalForceCompleteOnInternalChannelEmptyWorkflowCadence, TestConditionalForceCompleteOnInternalChannelEmptyWorkflowTemporalContinueAsNew, TestConditionalForceCompleteOnInternalChannelEmptyWorkflowCadenceContinueAsNew |
| [config.go](../../server/integ/config.go) | 13, 2, 14, 4, 15 | Shared integration harness / helper |
| [create_test.go](../../server/integ/create_test.go) | 1, 2 | TestCreateFlowTemporal, TestCreateFlowCadence, TestCreateFlowTemporalContinueAsNew, TestCreateFlowCadenceContinueAsNew |
| [deadend_test.go](../../server/integ/deadend_test.go) | 1, 2, 5, 6, 8 | TestDeadEndFlowTemporal, TestSynchronousUpdateRequestIDTemporal, TestDeadEndFlowCadence, TestDeadEndFlowTemporalContinueAsNew, TestDeadEndFlowCadenceContinueAsNew |
| [encryption_data_converter.go](../../server/integ/encryption_data_converter.go) | 5, 9 | Shared integration harness / helper |
| [external_dex.go](../../server/integ/external_dex.go) | 13, 1, 2, 16, 3, 4 | Shared integration harness / helper |
| [flow_id_passthrough_test.go](../../server/integ/flow_id_passthrough_test.go) | 1, 2, 14, 4 | TestFlowIDWithTenantPrefixPassesThroughTemporal |
| [from_step_execution_id_test.go](../../server/integ/from_step_execution_id_test.go) | 1, 2, 3, 4, 5, 6, 17, 8, 10, 11 | TestStepLineageTemporal, TestStepLineageCadence |
| [general_auth_test.go](../../server/integ/general_auth_test.go) | 13, 1, 2, 18, 19 | TestGeneralAuthenticationTemporal, TestGeneralAuthenticationCadence |
| [general_wake_test.go](../../server/integ/general_wake_test.go) | 13, 1, 2 | TestGeneralWakeTemporal, TestGeneralWakeCadence |
| [get_with_wait_timeout_test.go](../../server/integ/get_with_wait_timeout_test.go) | 1, 2 | TestFlowWaitForTimeoutTemporal, TestFlowWaitForTimeoutCadence |
| [greedy_timer_test.go](../../server/integ/greedy_timer_test.go) | 1, 2, 14 | TestGreedyTimerFlowBaseTemporal, TestGreedyTimerFlowBaseCadence, TestGreedyTimerFlowBaseTemporalContinueAsNew, TestGreedyTimerFlowBaseCadenceContinueAsNew |
| [hard_delete_attribute_test.go](../../server/integ/hard_delete_attribute_test.go) | 1, 2, 20, 4, 8, 10, 11 | TestHardDeleteAttributesTemporal, TestHardDeleteAttributesCadence, TestHardDeleteAttributeResetTemporal, TestHardDeleteAttributeResetCadence, TestHardDeleteAttributeSubFlowTemporal, TestHardDeleteAttributeSubFlowCadence, TestHardDeleteAttributeValidationTemporal, TestHardDeleteAttributeRequiresBlobStoreTemporal, TestHardDeleteAttributeStepWriteRequiresBlobStoreTemporal, TestHardDeleteAttributeBlobCacheTemporal, TestHardDeleteAttributeCleanupTemporal |
| [headers_test.go](../../server/integ/headers_test.go) | 1, 2, 4 | TestHeadersFlowTemporal, TestHeadersFlowCadence |
| [history_not_found_test.go](../../server/integ/history_not_found_test.go) | 1, 2 | TestGetHistoryEventsNotFoundTemporal, TestGetHistoryEventsNotFoundCadence |
| [internalchannel_test.go](../../server/integ/internalchannel_test.go) | 1, 2 | TestInterStateWorkflowTemporal, TestInterStateWorkflowCadence |
| [large_data_attributes_test.go](../../server/integ/large_data_attributes_test.go) | 1, 2, 4 | TestLargeDataAttributesTemporalContinueAsNew |
| [locking_test.go](../../server/integ/locking_test.go) | 1, 2 | TestLockingWorkflowTemporal, TestLockingWorkflowTemporalContinueAsNew, TestLockingWorkflowCadence, TestLockingWorkflowCadenceContinueAsNew |
| [main_test.go](../../server/integ/main_test.go) | 1, 2, 20, 4, 15, 21, 10 | TestMain |
| [matching_poll_test.go](../../server/integ/matching_poll_test.go) | 13, 1, 2, 20 | TestPullWorkerRoutingTemporal, TestPullWorkerRoutingCadence |
| [metrics_test.go](../../server/integ/metrics_test.go) | 1, 2, 15, 22 | TestTemporalMetrics |
| [no_workflow_id_test.go](../../server/integ/no_workflow_id_test.go) | 1, 2 | TestPublishToChannelNoFlowId |
| [parallel_test.go](../../server/integ/parallel_test.go) | 1, 2 | TestParallelFlowTemporal, TestParallelFlowCadence |
| [persistence_loading_policy_test.go](../../server/integ/persistence_loading_policy_test.go) | 1, 2 | TestPersistenceLoadingPolicy |
| [persistence_test.go](../../server/integ/persistence_test.go) | 1, 2, 23 | TestPersistenceWorkflowTemporal, TestPersistenceWorkflowTemporalWithEncryption, TestPersistenceWorkflowTemporalContinueAsNew, TestPersistenceWorkflowTemporalContinueAsNewWithEncryption, TestPersistenceWorkflowCadence, TestPersistenceWorkflowCadenceContinueAsNew |
| [pull_adapter.go](../../server/integ/pull_adapter.go) | 13, 1 | Shared integration harness / helper |
| [pull_adapter_test.go](../../server/integ/pull_adapter_test.go) | 1, 2 | TestPullAdapterReconnects, TestPullAdapterRegistrationTimeout, TestPullAdapterBasicTemporal, TestPullAdapterUnregisteredSubFlowTemporal, TestPullAdapterRejectsAmbiguousWorkersTemporal, TestPullWaitForAttributeSuccessTemporal |
| [pull_routing_test.go](../../server/integ/pull_routing_test.go) | 13, 1, 2, 4 | TestPullRoutingTemporal, TestPullRoutingCadence, TestPullSameWorkerFallbackTemporal, TestPullSameWorkerFallbackCadence, TestPullRPCRoutingTemporal, TestPullRPCSignalRoutingTemporal, TestPullRPCSignalRoutingCadence, TestPullRoutingReplayTemporal, TestPullAsyncWaitContinueAsNewReplayTemporal |
| [pull_target_validation_test.go](../../server/integ/pull_target_validation_test.go) | 1, 2 | TestPullWorkerTargetValidationTemporal, TestPullWorkerTargetValidationCadence |
| [pull_worker_test.go](../../server/integ/pull_worker_test.go) | 13, 1, 2, 20, 14 | TestPullWorkerDispatchTemporal, TestPullWorkerDispatchCadence, TestPullWorkerSplitRoleTemporal, TestPullWorkerSplitRoleCadence, TestPullWorkerAllInOneTemporal, TestPullWorkerAllInOneCadence, TestPullRPCWhenMatchingClosesTemporal, TestPullRPCWhenMatchingClosesCadence, TestPullWorkerCancellationTemporal, TestPullWorkerCancellationCadence, TestPullWorkerHeartbeatTimeoutTemporal, TestPullWorkerHeartbeatTimeoutCadence, TestPullWorkerUnavailableTemporal, TestPullDispatchFailureTextTemporal, TestPullDispatchFailureTextCadence, TestPullWorkerStickyFallbackTemporal, TestPullWorkerStickyFallbackCadence, TestPullWorkerAppearsDuringRetriesTemporal, TestPullWorkerAppearsDuringRetriesCadence, TestPullWorkerDisconnectTemporal, TestPullWorkerDisconnectCadence, TestPullWorkerStickyCapacityRecoveryTemporal, TestPullWorkerStickyCapacityRecoveryCadence, TestPullWorkerAsyncLocalTemporal, TestPullWorkerAsyncLocalCadence, TestPullSubFlowStartsWithoutStickyQueueTemporal, TestPullSubFlowStartsWithoutStickyQueueCadence, TestPushParentRoutesSubFlowToPullWorkerTemporal, TestPushParentRoutesSubFlowToPullWorkerCadence, TestPullSubFlowRejectsInvalidTargetTemporal, TestPullSubFlowRejectsInvalidTargetCadence, TestPullParallelLastWriteTemporal, TestPullParallelLastWriteCadence, TestPullWorkerQueueSurvivesContinueAsNewTemporal, TestPullWorkerQueueSurvivesContinueAsNewCadence, TestPullUnknownNameRedispatchTemporal, TestPullUnknownNameRedispatchCadence, TestPullUnknownRPCBoundedFailureTemporal, TestPullUnknownRPCBoundedFailureCadence, TestPullWorkerUnavailableCadence, TestPullWorkerErrorMatchesPushTemporal, TestPullWorkerErrorMatchesPushCadence, TestPullStepErrorMatchesPushTemporal, TestPullStepErrorMatchesPushCadence, TestPullRPCDispatchRespectsCallerDeadlineTemporal, TestPullRPCDispatchRespectsCallerDeadlineCadence, TestPullTransactionalRPCShortBudgetTemporal, TestPullTransactionalRPCTimeoutReleasesLockTemporal |
| [reset_by_state_id_test.go](../../server/integ/reset_by_state_id_test.go) | 1, 2 | TestResetByStateIdWorkflowTemporal, TestResetByStateIdWorkflowCadence |
| [rpc_external_storage_test.go](../../server/integ/rpc_external_storage_test.go) | 1, 2, 3, 4, 5, 6, 8 | TestRpcExternalStorageNonLockingTemporal, TestRpcExternalStorageSynchronousUpdateTemporal, TestRpcExternalStorageLockingTemporal, TestRpcExternalStorageNonLockingCadence, TestRpcSelectedChannelExternalStorageTemporal, TestRpcSelectedChannelExternalStorageCadence |
| [rpc_inline_transport_test.go](../../server/integ/rpc_inline_transport_test.go) | 1, 2, 3, 4, 5, 6, 8 | TestRpcReadBlobStoreUnavailableTemporal, TestRpcReadBlobStoreUnavailableCadence, TestRpcInlineTransportTemporal, TestRpcInlineTransportCadence, TestRpcTransactionalRequestTooLargeTemporal, TestRpcWorkerRequestTooLargeTemporal, TestRpcWorkerRequestTooLargeCadence, TestRpcSignalWireCompatibilityTemporal, TestRpcSignalWireCompatibilityCadence |
| [rpc_test.go](../../server/integ/rpc_test.go) | 1, 2 | TestRpcWorkflowTemporal, TestRpcWorkflowTemporalContinueAsNew, TestRpcWorkflowTemporalWithMemo, TestRpcWorkflowTemporalWithMemoAndEncryption, TestRpcWorkflowTemporalContinueAsNewWithMemo, TestRpcWorkflowTemporalContinueAsNewWithMemoAndEncryption, TestRpcWorkflowCadence, TestRpcWorkflowCadenceContinueAsNew, TestRpcLockingUnimplementedCadence, TestRpcLockingErrorMappingTemporal |
| [run_server_for_debug_test.go](../../server/integ/run_server_for_debug_test.go) | 2 | Shared integration harness / helper |
| [runtime.go](../../server/integ/runtime.go) | 13, 1, 2, 24, 20, 14, 25, 16, 26, 27, 3, 28, 19, 4, 29, 30, 31, 32, 33, 34, 35, 36, 37, 15, 9 | Shared integration harness / helper |
| [s3_cleanup_test.go](../../server/integ/s3_cleanup_test.go) | 1, 2, 27 | TestS3CleanupTemporal, TestS3CleanupCadence |
| [s3_get_set_data_attributes_api_test.go](../../server/integ/s3_get_set_data_attributes_api_test.go) | 1, 2, 4 | TestS3GetSetDataAttributesTemporal, TestS3GetSetDataAttributesCadence, TestS3GetSetDataAttributesWithInitialDataTemporal |
| [s3_history_assert.go](../../server/integ/s3_history_assert.go) | 14, 5, 6, 8 | Shared integration harness / helper |
| [s3_prefix_test.go](../../server/integ/s3_prefix_test.go) | 2, 20, 27 | TestS3KeyPrefixLayoutAndCleanupTemporal |
| [s3_wf_init_data_attributes_test.go](../../server/integ/s3_wf_init_data_attributes_test.go) | 1, 2, 4 | TestS3WorkflowInitDataAttributesTemporal, TestS3WorkflowInitDataAttributesCadence |
| [s3_wf_start_input_test.go](../../server/integ/s3_wf_start_input_test.go) | 1, 2, 4 | TestS3WorkflowStartInputTemporal, TestS3WorkflowStartInputCadence |
| [s3_wf_state_input_optimization_test.go](../../server/integ/s3_wf_state_input_optimization_test.go) | 1, 2, 4 | TestS3WorkflowStateInputOptimizationTemporal, TestS3WorkflowStateInputOptimizationCadence |
| [s3_wf_upsert_data_objects_test.go](../../server/integ/s3_wf_upsert_data_objects_test.go) | 1, 2, 4 | TestS3WorkflowUpsertDataObjectsTemporal, TestS3WorkflowUpsertDataObjectsCadence |
| [search_runs_test.go](../../server/integ/search_runs_test.go) | 1, 2, 20, 14, 4, 10 | TestSearchFlowsExcludeInternalWorkflowTemporal, TestSearchFlowsExcludeInternalWorkflowCadence, TestSearchRunsTemporal, TestSearchRunsCadence |
| [server_info_test.go](../../server/integ/server_info_test.go) | 1, 2 | TestServerInfoTemporal, TestServerInfoCadence |
| [set_data_attributes_test.go](../../server/integ/set_data_attributes_test.go) | 1, 2 | TestSetDataAttributesTemporal |
| [set_search_attributes_test.go](../../server/integ/set_search_attributes_test.go) | 1, 2 | TestSetSearchAttributes |
| [signal_test.go](../../server/integ/signal_test.go) | 1, 2, 4 | TestSignalWorkflowTemporal, TestSignalWorkflowTemporalContinueAsNew, TestSignalWorkflowCadence, TestSignalWorkflowCadenceContinueAsNew |
| [skip_start_test.go](../../server/integ/skip_start_test.go) | 1, 2 | TestSkipStartFlowTemporal, TestSkipStartFlowTemporalContinueAsNew, TestSkipStartFlowCadence, TestSkipStartFlowCadenceContinueAsNew |
| [slow_engine_task_read_test.go](../../server/integ/slow_engine_task_read_test.go) | 1, 2, 14 | TestReadBehindSlowEngineTaskTemporal |
| [start_delay_test.go](../../server/integ/start_delay_test.go) | 1, 2 | TestStartDelayTemporal, TestStartDelayCadence |
| [start_with_no_options_test.go](../../server/integ/start_with_no_options_test.go) | 1, 2 | TestStartFlowNoOptionsTemporal, TestStartFlowNoOptionsCadence |
| [step_cancellation_test.go](../../server/integ/step_cancellation_test.go) | 13, 1, 2, 4 | TestStepCancellationTemporal, TestStepCancellationCadence, TestQueuedStepCancellationAcrossContinueAsNewTemporal, TestQueuedStepCancellationAcrossContinueAsNewCadence, TestLocalStepCancellationTemporal, TestLocalStepCancellationCadence, TestLocalTimeoutFallbackCancellationTemporal, TestLocalTimeoutFallbackCancellationCadence, TestRpcStepCancellationTemporal, TestRpcStepCancellationSynchronousUpdateTemporal, TestRpcStepCancellationCadence |
| [step_state_loading_test.go](../../server/integ/step_state_loading_test.go) | 1, 2 | TestStepStateLoadingTemporal, TestStepStateLoadingCadence |
| [stream_test.go](../../server/integ/stream_test.go) | 13, 1, 2, 28, 30 | TestStreamStoreReversePaginationRedis, TestStreamStoreReversePaginationMemory, TestStreamStoreGlobalFIFOResumeAndRepeatedSource, TestStreamStoreFlowTypesIsolateStreamScopes, TestStreamStoreTrimWatermarks, TestStreamStoreTrimmedSourceCanWriteAgain, TestStreamStoreConcurrentTriggersUseSingletonTrimLease, TestStreamStoreMessageSizeLimit, TestStreamStoreMemoryResumeRepeatedSourceAndBlockingRead, TestStreamStoreMemoryTrimWatermarks, TestStreamStoreMemoryConcurrentCapacityTriggersDoNotOverTrim, TestStreamStoreMemoryResumeTokenSurvivesProcessRestartBestEffort, TestStreamStoreBackendConfiguration, TestStreamStepWorkerStreamingTemporal, TestStreamStepWorkerStreamingCadence, TestStreamAPITemporal, TestStreamFailureIsolationTemporal, TestStreamDisabledTemporal |
| [subflow_test.go](../../server/integ/subflow_test.go) | 1, 2, 4 | TestSubFlowConditionTemporal, TestSubFlowConditionCadence |
| [temporal_auth_test.go](../../server/integ/temporal_auth_test.go) | 13, 1, 2, 20, 4 | TestTemporalAuthTokenRotation, TestTemporalAuthAPIKeyFileStartupValidation, TestTemporalAuthCustomCAAndServerName, TestTemporalAuthClientCertificateRotation, TestTemporalAuthPlaintextKeyCompatibility |
| [terminal_producer_drain_test.go](../../server/integ/terminal_producer_drain_test.go) | 1, 2, 14 | TestTerminalProducerDrainTemporal, TestTerminalProducerDrainCadence |
| [timer_test.go](../../server/integ/timer_test.go) | 1, 2, 4 | TestTimerFlowTemporal, TestTimerFlowCadence, TestTimerFlowTemporalContinueAsNew, TestTimerFlowCadenceContinueAsNew, TestGreedyTimerFlowTemporal, TestGreedyTimerFlowCadence, TestGreedyTimerFlowTemporalContinueAsNew, TestGreedyTimerFlowCadenceContinueAsNew |
| [wait_for_attribute_test.go](../../server/integ/wait_for_attribute_test.go) | 1, 2, 4 | TestWaitForAttributeBlobBackedTemporal, TestWaitForAttributeTemporal, TestWaitForAttributeCadence |
| [wait_for_state_completion_test.go](../../server/integ/wait_for_state_completion_test.go) | 1, 2, 4 | TestWaitForStateCompletionTemporal, TestWaitForStateCompletionCadence |
| [wait_until_search_attributes_optimization_test.go](../../server/integ/wait_until_search_attributes_optimization_test.go) | 1, 2, 4, 5, 6, 17, 8 | TestWaitUntilSearchAttributesOptimizationWorkflowTemporal |
| [wait_until_search_attributes_test.go](../../server/integ/wait_until_search_attributes_test.go) | 1, 2, 4 | TestWaitUntilSearchAttributesWorkflowTemporal |
| [web_api_test.go](../../server/integ/web_api_test.go) | 1, 2, 27, 4 | TestWebAPITemporal, TestWebAPICadence |
| [wf_force_fail_test.go](../../server/integ/wf_force_fail_test.go) | 1, 2 | TestFlowForceFailTemporal, TestFlowForceFailCadence |
| [wf_ignore_already_started_test.go](../../server/integ/wf_ignore_already_started_test.go) | 1, 2 | TestIgnoreAlreadyStartedFlowTemporal, TestIgnoreAlreadyStartedFlowCadence |
| [wf_state_api_fail_test.go](../../server/integ/wf_state_api_fail_test.go) | 1, 2 | TestStateApiFailTemporal, TestStateApiFailCadence |
| [wf_state_api_timeout_test.go](../../server/integ/wf_state_api_timeout_test.go) | 1, 2 | TestStateApiTimeoutTemporal, TestStateApiTimeoutCadence |
| [wf_state_execute_method_fail_and_proceed_test.go](../../server/integ/wf_state_execute_method_fail_and_proceed_test.go) | 1, 2 | TestStateExecuteMethodFailAndProceedTemporal, TestStateExecuteMethodFailAndProceedCadence, TestStateExecuteTimeoutRecoveryTemporal, TestStateExecuteTimeoutRecoveryCadence |
| [wf_state_options_data_attributes_loading_test.go](../../server/integ/wf_state_options_data_attributes_loading_test.go) | 1, 2 | TestWfStateOptionsDataAttributesLoading |
| [wf_state_options_search_attributes_loading_test.go](../../server/integ/wf_state_options_search_attributes_loading_test.go) | 1, 2 | TestWfStateOptionsSearchAttributesLoading |
| [wf_state_wait_until_api_fail_and_proceed_test.go](../../server/integ/wf_state_wait_until_api_fail_and_proceed_test.go) | 1, 2 | TestStateApiFailAndProceedTemporal, TestStateApiFailAndProceedCadence, TestStateApiRetryAfterTemporal, TestStateApiRetryAfterCadenceFailsValidation |
| [wf_stop_test.go](../../server/integ/wf_stop_test.go) | 1, 2 | TestWorkflowCanceledTemporal, TestInvokeRPCUpdateRejectedAfterTerminalTemporal, TestInvokeRPCReadOnlyQueryAfterTerminalTemporal, TestInvokeRPCSignalRejectedAfterTerminalTemporal, TestWorkflowCanceledCadence |
| [wf_timeout_test.go](../../server/integ/wf_timeout_test.go) | 1, 2, 38, 4 | TestFlowTimeoutTemporal, TestFlowTimeoutCadence |
| [worker_target_update_test.go](../../server/integ/worker_target_update_test.go) | 1, 2 | TestUpdateWorkerTargetTemporal, TestUpdateWorkerTargetCadence |
| [workflow/any_command_close/routers.go](../../server/integ/workflow/any_command_close/routers.go) | 1 | Fixture package workflow/any_command_close |
| [workflow/any_command_combination/routers.go](../../server/integ/workflow/any_command_combination/routers.go) | 1 | Fixture package workflow/any_command_combination |
| [workflow/any_timer_signal/routers.go](../../server/integ/workflow/any_timer_signal/routers.go) | 1 | Fixture package workflow/any_timer_signal |
| [workflow/basic/routers.go](../../server/integ/workflow/basic/routers.go) | 1 | Fixture package workflow/basic |
| [workflow/channel_multivalue/routers.go](../../server/integ/workflow/channel_multivalue/routers.go) | 1, 4 | Fixture package workflow/channel_multivalue |
| [workflow/command_thread_completion/routers.go](../../server/integ/workflow/command_thread_completion/routers.go) | 1 | Fixture package workflow/command_thread_completion |
| [workflow/common/close_decision.go](../../server/integ/workflow/common/close_decision.go) | 1 | Fixture package workflow/common |
| [workflow/common/load_blobs.go](../../server/integ/workflow/common/load_blobs.go) | 1 | Fixture package workflow/common |
| [workflow/common/log.go](../../server/integ/workflow/common/log.go) | 28 | Fixture package workflow/common |
| [workflow/conditional_close/routers.go](../../server/integ/workflow/conditional_close/routers.go) | 1 | Fixture package workflow/conditional_close |
| [workflow/deadend/routers.go](../../server/integ/workflow/deadend/routers.go) | 1 | Fixture package workflow/deadend |
| [workflow/greedy_timer/routers.go](../../server/integ/workflow/greedy_timer/routers.go) | 1 | Fixture package workflow/greedy_timer |
| [workflow/hard-delete-attributes/routers.go](../../server/integ/workflow/hard-delete-attributes/routers.go) | 1 | Fixture package workflow/hard-delete-attributes |
| [workflow/headers/routers.go](../../server/integ/workflow/headers/routers.go) | 1 | Fixture package workflow/headers |
| [workflow/interstate/routers.go](../../server/integ/workflow/interstate/routers.go) | 1 | Fixture package workflow/interstate |
| [workflow/locking/routers.go](../../server/integ/workflow/locking/routers.go) | 1 | Fixture package workflow/locking |
| [workflow/parallel/routers.go](../../server/integ/workflow/parallel/routers.go) | 1 | Fixture package workflow/parallel |
| [workflow/persistence/routers.go](../../server/integ/workflow/persistence/routers.go) | 1 | Fixture package workflow/persistence |
| [workflow/persistence_loading_policy/routers.go](../../server/integ/workflow/persistence_loading_policy/routers.go) | 1 | Fixture package workflow/persistence_loading_policy |
| [workflow/reset/routers.go](../../server/integ/workflow/reset/routers.go) | 1 | Fixture package workflow/reset |
| [workflow/rpc/routers.go](../../server/integ/workflow/rpc/routers.go) | 1 | Fixture package workflow/rpc |
| [workflow/rpc-external-storage/routers.go](../../server/integ/workflow/rpc-external-storage/routers.go) | 1 | Fixture package workflow/rpc-external-storage |
| [workflow/s3-get-set-data-attributes/routers.go](../../server/integ/workflow/s3-get-set-data-attributes/routers.go) | 1 | Fixture package workflow/s3-get-set-data-attributes |
| [workflow/s3-init-data-attributes/routers.go](../../server/integ/workflow/s3-init-data-attributes/routers.go) | 1 | Fixture package workflow/s3-init-data-attributes |
| [workflow/s3-start-input/routers.go](../../server/integ/workflow/s3-start-input/routers.go) | 1 | Fixture package workflow/s3-start-input |
| [workflow/s3-state-input-optimization/routers.go](../../server/integ/workflow/s3-state-input-optimization/routers.go) | 1 | Fixture package workflow/s3-state-input-optimization |
| [workflow/s3-upsert-data-objects/routers.go](../../server/integ/workflow/s3-upsert-data-objects/routers.go) | 1 | Fixture package workflow/s3-upsert-data-objects |
| [workflow/signal/routers.go](../../server/integ/workflow/signal/routers.go) | 1 | Fixture package workflow/signal |
| [workflow/skipstart/routers.go](../../server/integ/workflow/skipstart/routers.go) | 1 | Fixture package workflow/skipstart |
| [workflow/step_cancellation/routers.go](../../server/integ/workflow/step_cancellation/routers.go) | 1, 4 | Fixture package workflow/step_cancellation |
| [workflow/timer/routers.go](../../server/integ/workflow/timer/routers.go) | 1 | Fixture package workflow/timer |
| [workflow/wait_for_state_completion/routers.go](../../server/integ/workflow/wait_for_state_completion/routers.go) | 1 | Fixture package workflow/wait_for_state_completion |
| [workflow/wait_until_search_attributes/routers.go](../../server/integ/workflow/wait_until_search_attributes/routers.go) | 1 | Fixture package workflow/wait_until_search_attributes |
| [workflow/wait_until_search_attributes_optimization/routers.go](../../server/integ/workflow/wait_until_search_attributes_optimization/routers.go) | 1 | Fixture package workflow/wait_until_search_attributes_optimization |
| [workflow/wf_execute_method_fail_and_proceed/routers.go](../../server/integ/workflow/wf_execute_method_fail_and_proceed/routers.go) | 1 | Fixture package workflow/wf_execute_method_fail_and_proceed |
| [workflow/wf_force_fail/routers.go](../../server/integ/workflow/wf_force_fail/routers.go) | 1 | Fixture package workflow/wf_force_fail |
| [workflow/wf_ignore_already_started/routers.go](../../server/integ/workflow/wf_ignore_already_started/routers.go) | 1 | Fixture package workflow/wf_ignore_already_started |
| [workflow/wf_state_api_fail/routers.go](../../server/integ/workflow/wf_state_api_fail/routers.go) | 1 | Fixture package workflow/wf_state_api_fail |
| [workflow/wf_state_api_fail_and_proceed/routers.go](../../server/integ/workflow/wf_state_api_fail_and_proceed/routers.go) | 1 | Fixture package workflow/wf_state_api_fail_and_proceed |
| [workflow/wf_state_api_timeout/routers.go](../../server/integ/workflow/wf_state_api_timeout/routers.go) | 1 | Fixture package workflow/wf_state_api_timeout |
| [workflow/wf_state_options_data_attributes_loading/routers.go](../../server/integ/workflow/wf_state_options_data_attributes_loading/routers.go) | 1 | Fixture package workflow/wf_state_options_data_attributes_loading |
| [workflow/wf_state_options_search_attributes_loading/routers.go](../../server/integ/workflow/wf_state_options_search_attributes_loading/routers.go) | 1 | Fixture package workflow/wf_state_options_search_attributes_loading |
