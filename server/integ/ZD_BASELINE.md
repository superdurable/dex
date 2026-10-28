# server/integ baseline against server-zd

This report records how the whole `server/integ` suite behaves when it runs in
binary mode against `dex-server-zd`. Every test has a result. Every skip and
failure has one classification.

- CI run: [Server Integration against server-zd #40642151961](https://github.com/superdurable/dex/actions/runs/40642151961)
  on commit `35b55fd74`, five partitions, debug `dex-server-zd`, PostgreSQL 17,
  and Redis 7.
- Command: `make -C server zdIntegTests dexServerZDBinary=<dex-server-zd> totalPartitions=5 partitionNum=<n>`.
- Regenerate the result table from the uploaded `zd-integ-json-*` artifacts with
  `server/script/summarize-integ-test-json.py --expected-tests <list>... <json>...`.

## Counts

| Result | Tests |
| --- | --- |
| pass | 95 |
| skip | 231 |
| fail | 0 |
| not run | 0 |
| total | 326 |

| Classification | Tests |
| --- | --- |
| go-server-only | 200 |
| harness-gap | 26 |
| behavior-difference | 4 |
| zd-bug | 2 |

The counts cover 231 skipped tests and one passing test with a skipped subtest
(**TestReadBehindSlowEngineTaskTemporal**). **TestBinaryTemporalOnlyScenarioSkipsBeforePolling**
counts as a pass because its skipped subtest is the asserted behavior.

The CI run has no failures because binary mode skips every scenario that it
cannot run. To find what those skips hide, the Temporal-only guards were
disabled locally on macOS with a temporary patch, and each guarded test ran in
its own process. These "forced runs" supply the evidence for the zd-bug and
harness-gap rows. The patch is not part of this change. Most guarded tests use
**require**, so a forced run stops at its first failure and does not exercise
the assertions after it.

## Classifications

- **zd-bug**: server-zd behaves wrongly.
- **behavior-difference**: a documented server-zd difference. The row proposes the assertion change.
- **go-server-only**: the test covers Temporal, Cadence, push Workers, Go Server configuration, or Go Server internals. It goes away with the Go Server.
- **harness-gap**: the binary-mode harness skips behavior that server-zd supports. The row proposes the harness fix.

C numbers refer to the server-zd compatibility differences that the binary-mode
guards already cite.

## zd-bug

1. **TestHardDeleteAttributeSubFlowTemporal** (high severity): a SubFlow
   started with a hard-delete Attribute in **SubFlowOptions.Attributes** stops
   the whole server node. Apply fails with
   `apply invariant violated: hard-delete write of "secret" has no uploaded object`,
   and the log ends with `dex-server-zd stopping after a failure`. The test then
   times out at `hard_delete_attribute_test.go:313`. To reproduce, delete the
   test's entry from **temporalOnlyScenarioTests** and run it in binary mode.
2. **TestLockingWorkflowTemporal** (low severity): an RPC that conflicts with a
   lock held by a running Step returns `ABORTED` with the expected sub-status,
   but its detail is
   `RPC lock conflict: attribute key CustomIntField is locked by a running Step`.
   The Go Server returns `one or more attribute keys are locked`
   (`locking_test.go:150`). The SDKs classify this error by code and sub-status,
   not by text. To reproduce, delete the test's entry from
   **temporalOnlyScenarioTests** and bypass **requireTemporalOnly** at
   `locking_test.go:83`. If the new text is intended, record it as a behavior
   difference and assert per engine.

Candidates from the forced **TestWebAPITemporal** run need triage before they
become issues:

- `sync-last-failure-*`, `sync-terminal-failure-*`, `sync-timeout-*`
  (`web_api_test.go:709` and its helpers): **StepMethodFailure.backend_error**
  holds the failure message, such as `retryable worker failure` or
  `method timeout of 1s expired`. The Go Server stores an error type, such as
  `FLOW_ERROR_TYPE_WORKER_API_FAIL` or `StartToClose`.
- `force-closed-pending-step` (`web_api_test.go:284`): the
  **StepMethodPendingEvent** written at Flow closure reports its input as
  unavailable.

## harness-gap

Fixed in this change:

- **TestDeadEndFlowTemporal** passes on server-zd unchanged. It is no longer
  listed as a Temporal-only scenario.
- **TestCreateFlowTemporal**, **TestCreateFlowTemporalContinueAsNew**, and
  **TestStartFlowNoOptionsTemporal** now guard only their Temporal query and
  describe assertions. They pass on server-zd.

Open, with proposed fixes:

- **Temporal-only assertions inside product scenarios** (13 tests): timer,
  greedy timer, signal, SubFlow condition, WaitForAttribute, search attribute,
  Attribute index, Step lineage, wait-for-Step-completion, read-behind, and
  command-combination scenarios first fail on server-zd when they call
  **QueryWorkflow**, **DescribeWorkflowExecution**, **GetApiService**, or
  **ListAttributeIndexes**. The assertions after that call were not exercised,
  so these are not yet small fixes. Guard the Temporal-only assertions with
  **isServerZDBinaryMode**, remove the tests from
  **temporalOnlyScenarioTests**, as this change does for the create-flow test,
  and triage what the rest of each test finds.
- **TestChannelMultivalueTemporal** reads channel contents from the Temporal
  debug dump. Read them through **GetChannelMessages** or **GetFlowState** in
  binary mode.
- **TestFlowTimeoutTemporal** passes the Flow timeout assertions of its first
  scenario, then fails at the Go Server in-process terminal event listener
  (`wf_timeout_test.go:233`). Guard that assertion in binary mode.
- **S3TestThreshold** (10 tests): binary mode skips every test that sets it.
  server-zd keeps large values in its log, and **values.wireInlineThresholdBytes**
  plays the same role (C5). Map **S3TestThreshold** to that setting, as the
  harness already does for **LocalBlobThreshold**. Then guard only the Go Blob
  Store object-count assertions, such as
  **globalBlobStore.CountWorkflowObjectsForTesting**.
- **TestWebAPITemporal**: 2 subtests pass, 14 skip, and 11 fail in the forced
  run. Split the Temporal-only parts and triage the candidates above.

## behavior-difference

- **TestStartDelayTemporal**: server-zd rejects `flow_start_delay_seconds > 0`
  with `InvalidArgument` (C8). Assert that error in binary mode.
- **TestStepStateLoadingTemporal**: the scenario orders its Steps with a start
  delay (C8). Order it with a WaitFor timer in binary mode.
- **TestHardDeleteAttributesTemporal**: the forced run passes the API and Worker
  assertions, then first fails at `hard_delete_attribute_test.go:139`, which
  counts files in the Go Server local Blob directory. server-zd stores
  hard-delete objects in its object store (C4, C5). Skip the Go Blob directory
  assertions in binary mode. The next **LoadBlobs** call uses a Go-format Blob
  ID and probably needs an engine-specific expectation too.
- **TestMongoDBAttributeSyncTemporal**: server-zd supports only PostgreSQL
  Attribute Stores (C26). Keep the guarded skip until server-zd adds MongoDB.

## go-server-only

| Reason | Tests |
| --- | --- |
| Cadence backend | 125 |
| In-process Go Server pull matching and dispatch internals | 34 |
| Continue-As-New (C3) | 9 |
| Temporal client, Update, Signal, memo, metrics, and direct workflow start | 14 |
| Go Server Blob Store, Blob cache, and async snapshot settings (C2, C5) | 11 |
| Temporal local-activity phase of ASYNC Steps (C2) | 2 |
| Push Workers (C18) and push Worker headers | 2 |
| Go Server Redis Stream Store backend | 1 |
| Go Server package default Flow configuration | 2 |

Some passing tests do not exercise server-zd. The **TestStreamStore** tests
call the Go Stream Store package directly, with Redis or memory. The
**TestBinary** tests check the harness itself. **TestTemporalAuthAPIKeyFileStartupValidation**
checks Go Server configuration.

## Per-test results

| Test | Result | Classification | Reason and evidence |
| --- | --- | --- | --- |
| TestAnyCommandCloseFlowCadence | skip | go-server-only | Skip reason: `any_command_close_test.go:52: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestAnyCommandCloseFlowCadenceContinueAsNew | skip | go-server-only | Skip reason: `any_command_close_test.go:62: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestAnyCommandCloseFlowTemporal | pass |  |  |
| TestAnyCommandCloseFlowTemporalContinueAsNew | pass |  |  |
| TestAnyCommandCombinationFlowCadence | skip | go-server-only | Skip reason: `any_command_combination_test.go:53: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestAnyCommandCombinationFlowCadenceContinueAsNew | skip | go-server-only | Skip reason: `any_command_combination_test.go:63: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestAnyCommandCombinationFlowTemporal | skip | harness-gap | Skip reason: `any_command_combination_test.go:82: Temporal-only assertion`. Whole test skipped in binary mode. A forced run first fails at the Temporal client call DescribeWorkflowExecution; later assertions were not exercised. Proposed fix: guard Temporal-only assertions with isServerZDBinaryMode, remove the test from temporalOnlyScenarioTests, and triage what the rest of the test finds. |
| TestAnyCommandCombinationFlowTemporalContinueAsNew | skip | go-server-only | Skip reason: `any_command_combination_test.go:82: Temporal-only assertion`. Continue-As-New variant; server-zd never continues a run as new (C3). |
| TestAnyCommandCompletedCadence | skip | go-server-only | Skip reason: `command_thread_completion_test.go:68: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestAnyCommandCompletedTemporal | pass |  |  |
| TestAnyTimerSignalFlowCadence | skip | go-server-only | Skip reason: `any_timer_signal_test.go:48: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestAnyTimerSignalFlowCadenceContinueAsNew | skip | go-server-only | Skip reason: `any_timer_signal_test.go:92: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestAnyTimerSignalFlowTemporal | pass |  |  |
| TestAnyTimerSignalFlowTemporalContinueAsNew | pass |  |  |
| TestAsyncRetryBudgetCadence | skip | go-server-only | Skip reason: `async_retry_budget_test.go:126: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestAsyncRetryBudgetTemporal | skip | go-server-only | Skip reason: `all 6 subtests skipped: async_retry_budget_test.go:342: Temporal-only assertion`. Tests the Temporal local-activity retry budget of ASYNC Steps (C2); a forced run panics on the missing Temporal client. |
| TestAttributeIndexSyncCadence | skip | go-server-only | Skip reason: `attribute_index_sync_test.go:34: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestAttributeIndexSyncTemporal | skip | harness-gap | Skip reason: `attribute_index_sync_test.go:40: Temporal-only assertion`. Whole test skipped in binary mode. A forced run first fails at the Temporal client call ListAttributeIndexes (Temporal search attributes); later assertions were not exercised. Proposed fix: guard Temporal-only assertions with isServerZDBinaryMode, remove the test from temporalOnlyScenarioTests, and triage what the rest of the test finds. |
| TestAttributeSyncCadence | skip | go-server-only | Skip reason: `attribute_sync_integ_test.go:45: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestAttributeSyncFlowTimeoutCadence | skip | go-server-only | Skip reason: `attribute_sync_integ_test.go:73: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestAttributeSyncFlowTimeoutTemporal | pass |  |  |
| TestAttributeSyncInvokeRPCGracefulCompleteTemporal | pass |  |  |
| TestAttributeSyncMapTablesCadence | skip | go-server-only | Skip reason: `attribute_sync_map_table_integ_test.go:37: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestAttributeSyncMapTablesTemporal | pass |  |  |
| TestAttributeSyncRetryExhaustionCadence | skip | go-server-only | Skip reason: `attribute_sync_integ_test.go:87: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestAttributeSyncRetryExhaustionTemporal | pass |  |  |
| TestAttributeSyncRowKeysCadence | skip | go-server-only | Skip reason: `attribute_sync_row_key_integ_test.go:37: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestAttributeSyncRowKeysTemporal | pass |  |  |
| TestAttributeSyncTemporal | pass |  |  |
| TestBasicFlowCadence | skip | go-server-only | Skip reason: `basic_test.go:57: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestBasicFlowTemporal | pass |  |  |
| TestBinaryDexConfigAttributeStoreMapping | pass |  |  |
| TestBinaryDexConfigMapping | pass |  |  |
| TestBinaryDexConfigUnsupportedSetting | pass |  |  |
| TestBinaryDexLauncher | pass |  |  |
| TestBinaryDexLauncherDisablesStreamsByDefault | pass |  |  |
| TestBinaryDexLauncherEarlyExit | pass |  |  |
| TestBinaryDexLauncherKillsUnresponsiveProcess | pass |  |  |
| TestBinaryDexLauncherReadinessTimeout | pass |  |  |
| TestBinaryTemporalOnlyScenarioSkipsBeforePolling | pass |  | Harness self-test; its subtest skip is the asserted behavior. |
| TestChannelMessageQueueCadence | skip | go-server-only | Skip reason: `channel_message_queue_test.go:48: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestChannelMessageQueueTemporal | pass |  |  |
| TestChannelMultivalueCadence | skip | go-server-only | Skip reason: `channel_multivalue_test.go:60: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestChannelMultivalueTemporal | skip | harness-gap | Skip reason: `channel_multivalue_test.go:319: Temporal-only assertion`. Whole test skipped in binary mode; its channel assertions read the Temporal debug-dump query. Proposed fix: read channel contents through GetChannelMessages or GetFlowState in binary mode. |
| TestCommandThreadCompletionCadence | skip | go-server-only | Skip reason: `command_thread_completion_test.go:46: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestCommandThreadCompletionTemporal | pass |  |  |
| TestConditionalForceCompleteOnInternalChannelEmptyWorkflowCadence | skip | go-server-only | Skip reason: `conditional_close_test.go:39: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestConditionalForceCompleteOnInternalChannelEmptyWorkflowCadenceContinueAsNew | skip | go-server-only | Skip reason: `conditional_close_test.go:63: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestConditionalForceCompleteOnInternalChannelEmptyWorkflowTemporal | pass |  |  |
| TestConditionalForceCompleteOnInternalChannelEmptyWorkflowTemporalContinueAsNew | pass |  |  |
| TestCreateFlowCadence | skip | go-server-only | Skip reason: `create_test.go:38: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestCreateFlowCadenceContinueAsNew | skip | go-server-only | Skip reason: `create_test.go:62: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestCreateFlowTemporal | pass |  |  |
| TestCreateFlowTemporalContinueAsNew | pass |  |  |
| TestDeadEndFlowCadence | skip | go-server-only | Skip reason: `deadend_test.go:53: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestDeadEndFlowCadenceContinueAsNew | skip | go-server-only | Skip reason: `deadend_test.go:77: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestDeadEndFlowTemporal | pass |  |  |
| TestDeadEndFlowTemporalContinueAsNew | skip | go-server-only | Skip reason: `deadend_test.go:185: Temporal-only assertion`. Continue-As-New variant; server-zd never continues a run as new (C3). |
| TestFlowForceFailCadence | skip | go-server-only | Skip reason: `wf_force_fail_test.go:39: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestFlowForceFailTemporal | pass |  |  |
| TestFlowIDWithTenantPrefixPassesThroughTemporal | skip | go-server-only | Skip reason: `flow_id_passthrough_test.go:87: Temporal-only assertion`. Starts the interpreter workflow directly through Temporal (StartInterpreterWorkflow). |
| TestFlowTimeoutCadence | skip | go-server-only | Skip reason: `wf_timeout_test.go:69: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestFlowTimeoutTemporal | skip | harness-gap | Skip reason: `wf_timeout_test.go:102: Temporal-only assertion`. Whole test skipped in binary mode. A forced run passes the Flow timeout assertions of its first scenario, then fails at the Go Server in-process terminal event listener (wf_timeout_test.go:233); later scenarios were not exercised. Proposed fix: guard the event-listener assertion in binary mode. |
| TestFlowWaitForTimeoutCadence | skip | go-server-only | Skip reason: `get_with_wait_timeout_test.go:39: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestFlowWaitForTimeoutTemporal | pass |  |  |
| TestForceCompleteIfChannelsEmptyCountsCommittedConsumptionCadence | skip | go-server-only | Skip reason: `conditional_close_committed_consumption_test.go:83: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestForceCompleteIfChannelsEmptyCountsCommittedConsumptionTemporal | pass |  |  |
| TestGeneralAuthenticationCadence | skip | go-server-only | Skip reason: `general_auth_test.go:47: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestGeneralAuthenticationTemporal | pass |  |  |
| TestGeneralWakeCadence | skip | go-server-only | Skip reason: `general_wake_test.go:41: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestGeneralWakeTemporal | skip | go-server-only | Skip reason: `general_wake_test.go:36: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestGetHistoryEventsNotFoundCadence | skip | go-server-only | Skip reason: `history_not_found_test.go:34: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestGetHistoryEventsNotFoundTemporal | pass |  |  |
| TestGreedyAnyTimerSignalFlowCadence | skip | go-server-only | Skip reason: `any_timer_signal_test.go:58: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestGreedyAnyTimerSignalFlowCadenceContinueAsNew | skip | go-server-only | Skip reason: `any_timer_signal_test.go:106: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestGreedyAnyTimerSignalFlowTemporal | pass |  |  |
| TestGreedyAnyTimerSignalFlowTemporalContinueAsNew | pass |  |  |
| TestGreedyTimerFlowBaseCadence | skip | go-server-only | Skip reason: `greedy_timer_test.go:40: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestGreedyTimerFlowBaseCadenceContinueAsNew | skip | go-server-only | Skip reason: `greedy_timer_test.go:60: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestGreedyTimerFlowBaseTemporal | skip | harness-gap | Skip reason: `greedy_timer_test.go:80: Temporal-only assertion`. Whole test skipped in binary mode. A forced run first fails at the Temporal client call QueryWorkflow (timer infos); later assertions were not exercised. Proposed fix: guard Temporal-only assertions with isServerZDBinaryMode, remove the test from temporalOnlyScenarioTests, and triage what the rest of the test finds. |
| TestGreedyTimerFlowBaseTemporalContinueAsNew | skip | go-server-only | Skip reason: `greedy_timer_test.go:80: Temporal-only assertion`. Continue-As-New variant; server-zd never continues a run as new (C3). |
| TestGreedyTimerFlowCadence | skip | go-server-only | Skip reason: `timer_test.go:82: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestGreedyTimerFlowCadenceContinueAsNew | skip | go-server-only | Skip reason: `timer_test.go:102: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestGreedyTimerFlowTemporal | skip | harness-gap | Skip reason: `timer_test.go:115: Temporal-only assertion`. Whole test skipped in binary mode. A forced run first fails at the Temporal client call QueryWorkflow (timer infos); later assertions were not exercised. Proposed fix: guard Temporal-only assertions with isServerZDBinaryMode, remove the test from temporalOnlyScenarioTests, and triage what the rest of the test finds. |
| TestGreedyTimerFlowTemporalContinueAsNew | skip | go-server-only | Skip reason: `timer_test.go:115: Temporal-only assertion`. Continue-As-New variant; server-zd never continues a run as new (C3). |
| TestHardDeleteAttributeBlobCacheTemporal | skip | go-server-only | Skip reason: `hard_delete_attribute_test.go:435: Temporal-only assertion`. Go Server Blob cache directory with S3 (C5). |
| TestHardDeleteAttributeCleanupTemporal | skip | go-server-only | Skip reason: `hard_delete_attribute_test.go:478: Temporal-only assertion`. Go Server hard-delete object cleanup in its local Blob directory; a forced run panics on a nil Go Blob Store. |
| TestHardDeleteAttributeRequiresBlobStoreTemporal | skip | go-server-only | Skip reason: `hard_delete_attribute_test.go:371: server-zd does not support disabled Blob Store`. Go Server Blob Store switch; server-zd always stores values in its log (C5). |
| TestHardDeleteAttributeResetCadence | skip | go-server-only | Skip reason: `hard_delete_attribute_test.go:230: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestHardDeleteAttributeResetTemporal | pass |  |  |
| TestHardDeleteAttributeStepWriteRequiresBlobStoreTemporal | skip | go-server-only | Skip reason: `hard_delete_attribute_test.go:395: Temporal-only assertion`. Go Server disabled Blob Store rejection (C5). |
| TestHardDeleteAttributeSubFlowCadence | skip | go-server-only | Skip reason: `hard_delete_attribute_test.go:288: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestHardDeleteAttributeSubFlowTemporal | skip | zd-bug | Skip reason: `hard_delete_attribute_test.go:295: Temporal-only assertion`. Skipped as a Temporal-only scenario. Removing it from temporalOnlyScenarioTests reproduces a node halt: starting a SubFlow whose SubFlowOptions.Attributes include a hard-delete Attribute fails apply with "apply invariant violated: hard-delete write of \"secret\" has no uploaded object", and dex-server-zd exits. High severity: one Flow start stops the whole node. |
| TestHardDeleteAttributeValidationTemporal | pass |  |  |
| TestHardDeleteAttributesCadence | skip | go-server-only | Skip reason: `hard_delete_attribute_test.go:68: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestHardDeleteAttributesTemporal | skip | behavior-difference | Skip reason: `all 4 subtests skipped: hard_delete_attribute_test.go:98: Temporal-only assertion`. A forced run passes the API and Worker assertions, then first fails at hard_delete_attribute_test.go:139, which counts files in the Go Server local Blob directory; later assertions were not exercised. server-zd stores hard-delete objects in its object store (C4, C5). Proposed change: skip the Go Blob directory assertions in binary mode, and check the following LoadBlobs call, which uses a Go-format Blob ID. |
| TestHeadersFlowCadence | skip | go-server-only | Skip reason: `headers_test.go:53: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestHeadersFlowTemporal | skip | go-server-only | Skip reason: `headers_test.go:74: server-zd does not support default Worker headers`. Go Server headers sent to push Workers. |
| TestIgnoreAlreadyStartedFlowCadence | skip | go-server-only | Skip reason: `wf_ignore_already_started_test.go:49: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestIgnoreAlreadyStartedFlowTemporal | pass |  |  |
| TestInterStateWorkflowCadence | skip | go-server-only | Skip reason: `internalchannel_test.go:45: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestInterStateWorkflowTemporal | pass |  |  |
| TestInvokeRPCReadOnlyQueryAfterTerminalTemporal | pass |  |  |
| TestInvokeRPCSignalRejectedAfterTerminalTemporal | pass |  |  |
| TestInvokeRPCUpdateRejectedAfterTerminalTemporal | skip | go-server-only | Skip reason: `all 2 subtests skipped: wf_stop_test.go:185: server-zd does not support Temporal synchronous RPC updates`. Temporal Update-based RPC delivery. |
| TestLargeDataAttributesTemporalContinueAsNew | skip | go-server-only | Skip reason: `all 2 subtests skipped: large_data_attributes_test.go:47: Temporal-only assertion`. Continue-As-New variant; server-zd never continues a run as new (C3). |
| TestLocalStepCancellationCadence | skip | go-server-only | Skip reason: `step_cancellation_test.go:65: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestLocalStepCancellationTemporal | pass |  |  |
| TestLocalTimeoutFallbackCancellationCadence | skip | go-server-only | Skip reason: `step_cancellation_test.go:81: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestLocalTimeoutFallbackCancellationTemporal | skip | go-server-only | Skip reason: `step_cancellation_test.go:75: server-zd does not support ASYNC local phase`. Temporal local-activity phase of ASYNC Steps; server-zd has none (C2). |
| TestLockingWorkflowCadence | skip | go-server-only | Skip reason: `locking_test.go:56: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestLockingWorkflowCadenceContinueAsNew | skip | go-server-only | Skip reason: `locking_test.go:66: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestLockingWorkflowTemporal | skip | zd-bug | Skip reason: `locking_test.go:83: Temporal-only assertion`. Skipped as a Temporal-only scenario. A forced run (remove it from temporalOnlyScenarioTests and bypass requireTemporalOnly at locking_test.go:83) fails at locking_test.go:150: the lock-conflict detail is "RPC lock conflict: attribute key CustomIntField is locked by a running Step", not "one or more attribute keys are locked". Code ABORTED and the sub-status match, and SDKs classify by those. Low severity. |
| TestLockingWorkflowTemporalContinueAsNew | skip | go-server-only | Skip reason: `locking_test.go:83: Temporal-only assertion`. Continue-As-New variant; server-zd never continues a run as new (C3). |
| TestMongoDBAttributeSyncCadence | skip | go-server-only | Skip reason: `attribute_sync_integ_test.go:59: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestMongoDBAttributeSyncTemporal | skip | behavior-difference | Skip reason: `attribute_sync_integ_test.go:345: server-zd does not support mongodb Attribute Store`. server-zd supports only PostgreSQL Attribute Stores (C26). Proposed change: none; keep the guarded skip until server-zd adds the store type. |
| TestParallelFlowCadence | skip | go-server-only | Skip reason: `parallel_test.go:45: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestParallelFlowTemporal | pass |  |  |
| TestPersistenceLoadingPolicy | pass |  |  |
| TestPersistenceWorkflowCadence | skip | go-server-only | Skip reason: `persistence_test.go:84: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestPersistenceWorkflowCadenceContinueAsNew | skip | go-server-only | Skip reason: `persistence_test.go:94: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestPersistenceWorkflowTemporal | pass |  |  |
| TestPersistenceWorkflowTemporalContinueAsNew | pass |  |  |
| TestPersistenceWorkflowTemporalContinueAsNewWithEncryption | skip | go-server-only | Skip reason: `persistence_test.go:115: server-zd does not support memo encryption`. Temporal memo encryption. |
| TestPersistenceWorkflowTemporalWithEncryption | skip | go-server-only | Skip reason: `persistence_test.go:115: server-zd does not support memo encryption`. Temporal memo encryption. |
| TestPublishToChannelNoFlowId | pass |  |  |
| TestPullAdapterBasicTemporal | pass |  |  |
| TestPullAdapterReconnects | pass |  |  |
| TestPullAdapterRegistrationTimeout | pass |  |  |
| TestPullAdapterRejectsAmbiguousWorkersTemporal | pass |  |  |
| TestPullAdapterUnregisteredSubFlowTemporal | pass |  |  |
| TestPullAsyncWaitContinueAsNewReplayTemporal | skip | go-server-only | Skip reason: `pull_routing_test.go:244: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullDispatchFailureTextCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:895: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullDispatchFailureTextTemporal | skip | go-server-only | Skip reason: `all 2 subtests skipped: pull_worker_test.go:903: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullParallelLastWriteCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1132: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullParallelLastWriteTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:1127: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullRPCDispatchRespectsCallerDeadlineCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1755: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullRPCDispatchRespectsCallerDeadlineTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:1750: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullRPCRoutingTemporal | skip | go-server-only | Skip reason: `pull_routing_test.go:110: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullRPCSignalRoutingCadence | skip | go-server-only | Skip reason: `pull_routing_test.go:157: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullRPCSignalRoutingTemporal | skip | go-server-only | Skip reason: `pull_routing_test.go:152: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullRPCWhenMatchingClosesCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:651: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullRPCWhenMatchingClosesTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:646: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullRoutingCadence | skip | go-server-only | Skip reason: `pull_routing_test.go:55: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullRoutingReplayTemporal | skip | go-server-only | Skip reason: `pull_routing_test.go:208: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullRoutingTemporal | skip | go-server-only | Skip reason: `all 2 subtests skipped: pull_routing_test.go:292: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullSameWorkerFallbackCadence | skip | go-server-only | Skip reason: `pull_routing_test.go:69: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullSameWorkerFallbackTemporal | skip | go-server-only | Skip reason: `all 2 subtests skipped: pull_routing_test.go:77: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullStepErrorMatchesPushCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1712: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullStepErrorMatchesPushTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:1707: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullSubFlowRejectsInvalidTargetCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1097: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullSubFlowRejectsInvalidTargetTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:1091: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullSubFlowStartsWithoutStickyQueueCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1050: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullSubFlowStartsWithoutStickyQueueTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:1045: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullTransactionalRPCShortBudgetTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:1764: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullTransactionalRPCTimeoutReleasesLockTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:1790: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullUnknownNameRedispatchCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1482: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullUnknownNameRedispatchTemporal | skip | go-server-only | Skip reason: `all 5 subtests skipped: pull_worker_test.go:1571: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullUnknownRPCBoundedFailureCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1497: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullUnknownRPCBoundedFailureTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:1491: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullWaitForAttributeSuccessTemporal | skip | harness-gap | Skip reason: `pull_adapter_test.go:170: Temporal-only assertion`. Whole test skipped in binary mode. A forced run first fails at the Temporal client call DescribeWorkflowExecution; later assertions were not exercised. Proposed fix: guard Temporal-only assertions with isServerZDBinaryMode, remove the test from temporalOnlyScenarioTests, and triage what the rest of the test finds. |
| TestPullWorkerAllInOneCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:637: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerAllInOneTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:631: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullWorkerAppearsDuringRetriesCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:994: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerAppearsDuringRetriesTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:989: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullWorkerAsyncLocalCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1036: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerAsyncLocalTemporal | skip | go-server-only | Skip reason: `all 3 subtests skipped: pull_worker_test.go:1212: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullWorkerCancellationCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:719: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerCancellationTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:714: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullWorkerDisconnectCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1008: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerDisconnectTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:1003: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullWorkerDispatchCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:607: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerDispatchTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:602: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullWorkerErrorMatchesPushCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1698: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerErrorMatchesPushTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:1693: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullWorkerHeartbeatTimeoutCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:733: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerHeartbeatTimeoutTemporal | skip | go-server-only | Skip reason: `all 4 subtests skipped: pull_worker_test.go:749: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullWorkerQueueSurvivesContinueAsNewCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1285: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerQueueSurvivesContinueAsNewTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:1280: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullWorkerRoutingCadence | skip | go-server-only | Skip reason: `matching_poll_test.go:104: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerRoutingTemporal | skip | go-server-only | Skip reason: `matching_poll_test.go:98: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullWorkerSplitRoleCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:622: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerSplitRoleTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:616: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullWorkerStickyCapacityRecoveryCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1022: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerStickyCapacityRecoveryTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:1017: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullWorkerStickyFallbackCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:980: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerStickyFallbackTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:975: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullWorkerTargetValidationCadence | skip | go-server-only | Skip reason: `pull_target_validation_test.go:33: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerTargetValidationTemporal | skip | go-server-only | Skip reason: `pull_target_validation_test.go:28: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPullWorkerUnavailableCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1684: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerUnavailableTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:883: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestPushParentRoutesSubFlowToPullWorkerCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1064: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPushParentRoutesSubFlowToPullWorkerTemporal | skip | go-server-only | Skip reason: `pull_worker_test.go:1059: server-zd does not support Go Server internals`. Starts the in-process Go Server to test its pull matching and dispatch internals; server-zd dispatch is covered by server-zd tests. |
| TestQueuedStepCancellationAcrossContinueAsNewCadence | skip | go-server-only | Skip reason: `step_cancellation_test.go:51: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestQueuedStepCancellationAcrossContinueAsNewTemporal | skip | go-server-only | Skip reason: `step_cancellation_test.go:45: server-zd does not support Continue-As-New`. Continue-As-New; server-zd never continues a run as new (C3). |
| TestReadBehindSlowEngineTaskTemporal | pass | harness-gap | Skip reason: `1 of 3 subtests skipped: slow_engine_task_read_test.go:188: Temporal-only assertion`. Whole test skipped in binary mode. A forced run first fails at the Temporal client call QueryWorkflow; later assertions were not exercised. Proposed fix: guard Temporal-only assertions with isServerZDBinaryMode, remove the test from temporalOnlyScenarioTests, and triage what the rest of the test finds. |
| TestResetByStateIdWorkflowCadence | skip | go-server-only | Skip reason: `reset_by_state_id_test.go:39: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestResetByStateIdWorkflowTemporal | pass |  |  |
| TestRpcExternalStorageLockingTemporal | skip | harness-gap | Skip reason: `all 2 subtests skipped: rpc_external_storage_test.go:175: Temporal-only assertion`. S3TestThreshold test; see the S3 threshold harness gap. |
| TestRpcExternalStorageNonLockingCadence | skip | go-server-only | Skip reason: `rpc_external_storage_test.go:74: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestRpcExternalStorageNonLockingTemporal | skip | harness-gap | Skip reason: `all 2 subtests skipped: rpc_external_storage_test.go:175: Temporal-only assertion`. S3TestThreshold test; see the S3 threshold harness gap. |
| TestRpcExternalStorageSynchronousUpdateTemporal | skip | go-server-only | Skip reason: `all 2 subtests skipped: rpc_external_storage_test.go:175: Temporal-only assertion`. Temporal Update-based RPC delivery with S3 Blob Store. |
| TestRpcInlineTransportCadence | skip | go-server-only | Skip reason: `rpc_inline_transport_test.go:152: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestRpcInlineTransportTemporal | skip | go-server-only | Skip reason: `all 8 subtests skipped: rpc_inline_transport_test.go:167: Temporal-only assertion`. Uses the Go Server async Step input snapshot setting (C2, C5). |
| TestRpcLockingErrorMappingTemporal | pass |  |  |
| TestRpcLockingUnimplementedCadence | skip | go-server-only | Skip reason: `rpc_test.go:112: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestRpcReadBlobStoreUnavailableCadence | skip | go-server-only | Skip reason: `rpc_inline_transport_test.go:83: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestRpcReadBlobStoreUnavailableTemporal | skip | go-server-only | Skip reason: `all 4 subtests skipped: rpc_inline_transport_test.go:100: Temporal-only assertion`. Uses the Go Server async Step input snapshot setting and Blob Store outages (C5). |
| TestRpcSelectedChannelExternalStorageCadence | skip | go-server-only | Skip reason: `rpc_external_storage_test.go:94: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestRpcSelectedChannelExternalStorageTemporal | skip | harness-gap | Skip reason: `all 2 subtests skipped: rpc_external_storage_test.go:102: server-zd does not support S3 Blob Store`. Binary mode skips every S3TestThreshold test. server-zd keeps large values in its log and uses values.wireInlineThresholdBytes for the same threshold (C5). Proposed fix: map S3TestThreshold to values.wireInlineThresholdBytes like LocalBlobThreshold, and guard only the Go Blob Store object-count assertions. |
| TestRpcSignalWireCompatibilityCadence | skip | go-server-only | Skip reason: `rpc_inline_transport_test.go:320: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestRpcSignalWireCompatibilityTemporal | skip | go-server-only | Skip reason: `rpc_inline_transport_test.go:315: Temporal-only assertion`. Decodes Temporal Signal payloads. |
| TestRpcStepCancellationCadence | skip | go-server-only | Skip reason: `step_cancellation_test.go:102: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestRpcStepCancellationSynchronousUpdateTemporal | skip | go-server-only | Skip reason: `step_cancellation_test.go:288: server-zd does not support Temporal synchronous RPC updates`. Temporal Update-based RPC delivery. |
| TestRpcStepCancellationTemporal | pass |  |  |
| TestRpcTransactionalRequestTooLargeTemporal | skip | go-server-only | Skip reason: `all 6 subtests skipped: rpc_inline_transport_test.go:207: Temporal-only assertion`. Uses the Go Server async Step input snapshot setting (C2, C5). |
| TestRpcWorkerRequestTooLargeCadence | skip | go-server-only | Skip reason: `rpc_inline_transport_test.go:257: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestRpcWorkerRequestTooLargeTemporal | skip | go-server-only | Skip reason: `all 4 subtests skipped: rpc_inline_transport_test.go:271: Temporal-only assertion`. Uses the Go Server async Step input snapshot setting (C2, C5). |
| TestRpcWorkflowCadence | skip | go-server-only | Skip reason: `rpc_test.go:92: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestRpcWorkflowCadenceContinueAsNew | skip | go-server-only | Skip reason: `rpc_test.go:102: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestRpcWorkflowTemporal | pass |  |  |
| TestRpcWorkflowTemporalContinueAsNew | pass |  |  |
| TestRpcWorkflowTemporalContinueAsNewWithMemo | pass |  |  |
| TestRpcWorkflowTemporalContinueAsNewWithMemoAndEncryption | pass |  |  |
| TestRpcWorkflowTemporalWithMemo | pass |  |  |
| TestRpcWorkflowTemporalWithMemoAndEncryption | pass |  |  |
| TestS3CleanupCadence | skip | go-server-only | Skip reason: `s3_cleanup_test.go:40: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestS3CleanupTemporal | skip | go-server-only | Skip reason: `s3_cleanup_test.go:49: Temporal-only assertion`. Go Server Blob Store cleanup objects in MinIO (C5). |
| TestS3GetSetDataAttributesCadence | skip | go-server-only | Skip reason: `s3_get_set_data_attributes_api_test.go:44: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestS3GetSetDataAttributesTemporal | skip | harness-gap | Skip reason: `all 2 subtests skipped: s3_get_set_data_attributes_api_test.go:71: Temporal-only assertion`. S3TestThreshold test; see the S3 threshold harness gap. |
| TestS3GetSetDataAttributesWithInitialDataTemporal | skip | harness-gap | Skip reason: `all 2 subtests skipped: s3_get_set_data_attributes_api_test.go:207: Temporal-only assertion`. S3TestThreshold test; see the S3 threshold harness gap. |
| TestS3KeyPrefixLayoutAndCleanupTemporal | skip | go-server-only | Skip reason: `s3_prefix_test.go:32: Temporal-only assertion`. Go Server Blob Store key layout in MinIO (C5). |
| TestS3WorkflowInitDataAttributesCadence | skip | go-server-only | Skip reason: `s3_wf_init_data_attributes_test.go:44: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestS3WorkflowInitDataAttributesTemporal | skip | harness-gap | Skip reason: `all 2 subtests skipped: s3_wf_init_data_attributes_test.go:57: Temporal-only assertion`. S3TestThreshold test; see the S3 threshold harness gap. |
| TestS3WorkflowStartInputCadence | skip | go-server-only | Skip reason: `s3_wf_start_input_test.go:44: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestS3WorkflowStartInputTemporal | skip | harness-gap | Skip reason: `all 2 subtests skipped: s3_wf_start_input_test.go:57: Temporal-only assertion`. S3TestThreshold test; see the S3 threshold harness gap. |
| TestS3WorkflowStateInputOptimizationCadence | skip | go-server-only | Skip reason: `s3_wf_state_input_optimization_test.go:43: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestS3WorkflowStateInputOptimizationTemporal | skip | harness-gap | Skip reason: `all 2 subtests skipped: s3_wf_state_input_optimization_test.go:56: Temporal-only assertion`. S3TestThreshold test; see the S3 threshold harness gap. |
| TestS3WorkflowUpsertDataObjectsCadence | skip | go-server-only | Skip reason: `s3_wf_upsert_data_objects_test.go:44: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestS3WorkflowUpsertDataObjectsTemporal | skip | harness-gap | Skip reason: `all 2 subtests skipped: s3_wf_upsert_data_objects_test.go:57: Temporal-only assertion`. S3TestThreshold test; see the S3 threshold harness gap. |
| TestSearchFlowsExcludeInternalWorkflowCadence | skip | go-server-only | Skip reason: `search_runs_test.go:40: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestSearchFlowsExcludeInternalWorkflowTemporal | skip | go-server-only | Skip reason: `search_runs_test.go:46: server-zd does not support internal Blob Store cleanup workflows`. Go Server Blob Store cleanup workflows (C5). |
| TestSearchRunsCadence | skip | go-server-only | Skip reason: `search_runs_test.go:129: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestSearchRunsTemporal | pass |  |  |
| TestServerInfoCadence | skip | go-server-only | Skip reason: `server_info_test.go:31: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestServerInfoTemporal | pass |  |  |
| TestSetDataAttributesTemporal | pass |  |  |
| TestSetSearchAttributes | skip | harness-gap | Skip reason: `set_search_attributes_test.go:37: Temporal-only assertion`. Whole test skipped in binary mode. A forced run first fails at the Temporal client call DescribeWorkflowExecution (search attributes); later assertions were not exercised. Proposed fix: guard Temporal-only assertions with isServerZDBinaryMode, remove the test from temporalOnlyScenarioTests, and triage what the rest of the test finds. |
| TestSignalWorkflowCadence | skip | go-server-only | Skip reason: `signal_test.go:52: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestSignalWorkflowCadenceContinueAsNew | skip | go-server-only | Skip reason: `signal_test.go:62: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestSignalWorkflowTemporal | skip | harness-gap | Skip reason: `signal_test.go:75: Temporal-only assertion`. Whole test skipped in binary mode. A forced run first fails at the Temporal client call QueryWorkflow; later assertions were not exercised. Proposed fix: guard Temporal-only assertions with isServerZDBinaryMode, remove the test from temporalOnlyScenarioTests, and triage what the rest of the test finds. |
| TestSignalWorkflowTemporalContinueAsNew | skip | go-server-only | Skip reason: `signal_test.go:75: Temporal-only assertion`. Continue-As-New variant; server-zd never continues a run as new (C3). |
| TestSkipStartFlowCadence | skip | go-server-only | Skip reason: `skip_start_test.go:48: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestSkipStartFlowCadenceContinueAsNew | skip | go-server-only | Skip reason: `skip_start_test.go:58: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestSkipStartFlowTemporal | pass |  |  |
| TestSkipStartFlowTemporalContinueAsNew | pass |  |  |
| TestStartDelayCadence | skip | go-server-only | Skip reason: `start_delay_test.go:36: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestStartDelayTemporal | skip | behavior-difference | Skip reason: `start_delay_test.go:30: server-zd does not support Flow start delay`. server-zd rejects flow_start_delay_seconds > 0 with InvalidArgument (C8). Proposed change: in binary mode assert InvalidArgument instead of skipping. |
| TestStartFlowNoOptionsCadence | skip | go-server-only | Skip reason: `start_with_no_options_test.go:34: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestStartFlowNoOptionsTemporal | pass |  |  |
| TestStateApiFailAndProceedCadence | skip | go-server-only | Skip reason: `wf_state_wait_until_api_fail_and_proceed_test.go:41: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestStateApiFailAndProceedTemporal | pass |  |  |
| TestStateApiFailCadence | skip | go-server-only | Skip reason: `wf_state_api_fail_test.go:51: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestStateApiFailTemporal | pass |  |  |
| TestStateApiRetryAfterCadenceFailsValidation | skip | go-server-only | Skip reason: `wf_state_wait_until_api_fail_and_proceed_test.go:151: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestStateApiRetryAfterTemporal | pass |  |  |
| TestStateApiTimeoutCadence | skip | go-server-only | Skip reason: `wf_state_api_timeout_test.go:39: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestStateApiTimeoutTemporal | pass |  |  |
| TestStateExecuteMethodFailAndProceedCadence | skip | go-server-only | Skip reason: `wf_state_execute_method_fail_and_proceed_test.go:63: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestStateExecuteMethodFailAndProceedTemporal | pass |  |  |
| TestStateExecuteTimeoutRecoveryCadence | skip | go-server-only | Skip reason: `wf_state_execute_method_fail_and_proceed_test.go:106: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestStateExecuteTimeoutRecoveryTemporal | pass |  |  |
| TestStepCancellationCadence | skip | go-server-only | Skip reason: `step_cancellation_test.go:35: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestStepCancellationTemporal | pass |  |  |
| TestStepLineageCadence | skip | go-server-only | Skip reason: `from_step_execution_id_test.go:53: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestStepLineageTemporal | skip | harness-gap | Skip reason: `all 3 subtests skipped: from_step_execution_id_test.go:83: Temporal-only assertion`. Whole test skipped in binary mode. A forced run first fails at the Temporal client call GetApiService; later assertions were not exercised. Proposed fix: guard Temporal-only assertions with isServerZDBinaryMode, remove the test from temporalOnlyScenarioTests, and triage what the rest of the test finds. |
| TestStepStateLoadingCadence | skip | go-server-only | Skip reason: `step_state_loading_test.go:144: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestStepStateLoadingTemporal | skip | behavior-difference | Skip reason: `step_state_loading_test.go:136: server-zd does not support Flow start delay`. The scenario orders its Steps with a Flow start delay, which server-zd rejects with InvalidArgument (C8). Proposed change: order the scenario with a WaitFor timer in binary mode so the Step state loading assertions run. |
| TestStreamAPITemporal | pass |  |  |
| TestStreamDisabledTemporal | pass |  |  |
| TestStreamFailureIsolationTemporal | skip | go-server-only | Skip reason: `stream_test.go:1080: server-zd does not support external Stream Store`. Go Server Redis Stream Store backend; server-zd supports only disabled and memory backends. |
| TestStreamStepWorkerStreamingCadence | skip | go-server-only | Skip reason: `stream_test.go:785: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestStreamStepWorkerStreamingTemporal | pass |  |  |
| TestStreamStoreBackendConfiguration | pass |  |  |
| TestStreamStoreConcurrentTriggersUseSingletonTrimLease | pass |  |  |
| TestStreamStoreFlowTypesIsolateStreamScopes | pass |  |  |
| TestStreamStoreGlobalFIFOResumeAndRepeatedSource | pass |  |  |
| TestStreamStoreMemoryConcurrentCapacityTriggersDoNotOverTrim | pass |  |  |
| TestStreamStoreMemoryResumeRepeatedSourceAndBlockingRead | pass |  |  |
| TestStreamStoreMemoryResumeTokenSurvivesProcessRestartBestEffort | pass |  |  |
| TestStreamStoreMemoryTrimWatermarks | pass |  |  |
| TestStreamStoreMessageSizeLimit | pass |  |  |
| TestStreamStoreReversePaginationMemory | pass |  |  |
| TestStreamStoreReversePaginationRedis | pass |  |  |
| TestStreamStoreTrimWatermarks | pass |  |  |
| TestStreamStoreTrimmedSourceCanWriteAgain | pass |  |  |
| TestSubFlowConditionCadence | skip | go-server-only | Skip reason: `subflow_test.go:57: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestSubFlowConditionTemporal | skip | harness-gap | Skip reason: `all 2 subtests skipped: subflow_test.go:90: Temporal-only assertion`. Whole test skipped in binary mode. A forced run first fails at the Temporal client call DescribeWorkflowExecution; later assertions were not exercised. Proposed fix: guard Temporal-only assertions with isServerZDBinaryMode, remove the test from temporalOnlyScenarioTests, and triage what the rest of the test finds. |
| TestSynchronousUpdateRequestIDTemporal | skip | go-server-only | Skip reason: `deadend_test.go:92: Temporal-only assertion`. Temporal Update request IDs. |
| TestTemporalAuthAPIKeyFileStartupValidation | pass |  |  |
| TestTemporalAuthClientCertificateRotation | skip | go-server-only | Skip reason: `temporal_auth_test.go:117: server-zd does not support Temporal client authentication`. Temporal client TLS and API key settings. |
| TestTemporalAuthCustomCAAndServerName | skip | go-server-only | Skip reason: `temporal_auth_test.go:97: server-zd does not support Temporal client authentication`. Temporal client TLS and API key settings. |
| TestTemporalAuthPlaintextKeyCompatibility | skip | go-server-only | Skip reason: `temporal_auth_test.go:148: server-zd does not support Temporal client authentication`. Temporal client TLS and API key settings. |
| TestTemporalAuthTokenRotation | skip | go-server-only | Skip reason: `temporal_auth_test.go:38: server-zd does not support Temporal client authentication`. Temporal client TLS and API key settings. |
| TestTemporalMetrics | skip | go-server-only | Skip reason: `metrics_test.go:49: server-zd does not support Temporal metrics handler`. Temporal SDK metrics. |
| TestTerminalProducerDrainCadence | skip | go-server-only | Skip reason: `terminal_producer_drain_test.go:111: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestTerminalProducerDrainTemporal | skip | go-server-only | Skip reason: `all 3 subtests skipped: terminal_producer_drain_test.go:150: server-zd does not support Temporal synchronous RPC updates`. Temporal Update-based RPC delivery. |
| TestTimerFlowCadence | skip | go-server-only | Skip reason: `timer_test.go:41: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestTimerFlowCadenceContinueAsNew | skip | go-server-only | Skip reason: `timer_test.go:61: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestTimerFlowTemporal | skip | harness-gap | Skip reason: `timer_test.go:115: Temporal-only assertion`. Whole test skipped in binary mode. A forced run first fails at the Temporal client call QueryWorkflow (timer infos); later assertions were not exercised. Proposed fix: guard Temporal-only assertions with isServerZDBinaryMode, remove the test from temporalOnlyScenarioTests, and triage what the rest of the test finds. |
| TestTimerFlowTemporalContinueAsNew | skip | go-server-only | Skip reason: `timer_test.go:115: Temporal-only assertion`. Continue-As-New variant; server-zd never continues a run as new (C3). |
| TestUpdateWorkerTargetCadence | skip | go-server-only | Skip reason: `worker_target_update_test.go:35: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestUpdateWorkerTargetTemporal | skip | go-server-only | Skip reason: `worker_target_update_test.go:41: requires push Workers: switching addresses between Workers for one Flow type`. Push Worker address switching; server-zd supports only pull Workers (C18). |
| TestWaitForAttributeBlobBackedTemporal | skip | harness-gap | Skip reason: `wait_for_attribute_test.go:92: server-zd does not support S3 Blob Store`. S3TestThreshold test; see the S3 threshold harness gap. |
| TestWaitForAttributeCadence | skip | go-server-only | Skip reason: `wait_for_attribute_test.go:82: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestWaitForAttributeTemporal | skip | harness-gap | Skip reason: `wait_for_attribute_test.go:51: Temporal-only assertion`. Whole test skipped in binary mode. A forced run first fails at the Temporal client call DescribeWorkflowExecution; later assertions were not exercised. Proposed fix: guard Temporal-only assertions with isServerZDBinaryMode, remove the test from temporalOnlyScenarioTests, and triage what the rest of the test finds. |
| TestWaitForStateCompletionCadence | skip | go-server-only | Skip reason: `wait_for_state_completion_test.go:67: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestWaitForStateCompletionTemporal | skip | harness-gap | Skip reason: `wait_for_state_completion_test.go:84: Temporal-only assertion`. Whole test skipped in binary mode. A forced run first fails at the Temporal client call GetApiService; later assertions were not exercised. Proposed fix: guard Temporal-only assertions with isServerZDBinaryMode, remove the test from temporalOnlyScenarioTests, and triage what the rest of the test finds. |
| TestWaitUntilSearchAttributesOptimizationWorkflowTemporal | skip | go-server-only | Skip reason: `wait_until_search_attributes_optimization_test.go:72: Temporal-only assertion`. Exercises the Go Server package default Flow configuration. |
| TestWaitUntilSearchAttributesWorkflowTemporal | skip | go-server-only | Skip reason: `wait_until_search_attributes_test.go:67: server-zd does not support built-in default Flow configuration`. Exercises the Go Server package default Flow configuration. |
| TestWebAPICadence | skip | go-server-only | Skip reason: `web_api_test.go:48: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestWebAPITemporal | skip | harness-gap | Skip reason: `all 26 subtests skipped: web_api_test.go:1483: Temporal-only assertion`. Whole test skipped in binary mode. A forced run: 2 subtests pass, 14 skip, 11 fail. Failures mix Temporal-only calls (DescribeWorkflowExecution), Go async snapshot settings (C2), and history differences listed under zd-bug candidates. Proposed fix: split the subtests and guard the Temporal-only parts. |
| TestWfStateOptionsDataAttributesLoading | pass |  |  |
| TestWfStateOptionsSearchAttributesLoading | pass |  |  |
| TestWorkflowCanceledCadence | skip | go-server-only | Skip reason: `wf_stop_test.go:102: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestWorkflowCanceledTemporal | pass |  |  |
