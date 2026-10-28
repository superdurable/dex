# server/integ baseline against server-zd

This report records how the whole `server/integ` suite behaves when it runs in
binary mode against `dex-server-zd`. Every test has a result. Every skip and
failure has one classification.

- CI run: [Server Integration against server-zd #40738057920](https://github.com/superdurable/dex/actions/runs/40738057920)
  on commit `4da24c222`, five partitions, debug `dex-server-zd`, PostgreSQL 17,
  and Redis 7.
- Command: `make -C server zdIntegTests dexServerZDBinary=<dex-server-zd> totalPartitions=5 partitionNum=<n>`.
- Regenerate the result table from the uploaded `zd-integ-json-*` artifacts with
  `server/script/summarize-integ-test-json.py --expected-tests <list>... <json>...`.

## Counts

| Result | Tests |
| --- | --- |
| pass | 149 |
| skip | 177 |
| fail | 0 |
| not run | 0 |
| total | 326 |

| Classification | Tests |
| --- | --- |
| go-server-only | 176 |
| zd-bug | 2 |
| behavior-difference | 11 |
| harness-gap | 1 |

The classification counts cover every row that has a classification: skipped
tests, passing tests with a skipped subtest, and passing tests whose binary-mode
assertions pin a zd-bug or a behavior difference. The summarizer counts the
harness self-test **TestBinaryTemporalOnlyScenarioSkipsBeforePolling** as a skip
because its only subtest skips. That skip is the asserted behavior, so the test
has no classification.

The first baseline had 94 passes and 232 skips. This change ports 26 tests that
were classified go-server-only, because they cover application-visible
behavior. It fixes the harness gaps for 26 more and turns 2 behavior-difference
skips into assertions. A forced run, with the
Temporal-only guards disabled locally, supplied the evidence for zd-bugs 1 and 2. The
other zd-bugs come from the ported tests themselves. #1011 fixed zd-bug 1, so
**TestHardDeleteAttributeSubFlowTemporal** now runs in binary mode and passes.
#1037, #1038, and #1019 fixed zd-bugs 4 to 7. Their guards are deleted, so
both engines run the Go Server assertions. The counts include these changes.

## Classifications

- **zd-bug**: server-zd behaves wrongly. A guard names the bug and pins the
  current server-zd result, so the test fails once server-zd changes. Only
  the guards for bugs 2 and 3 remain. When a bug is fixed, its guard is
  deleted and binary mode runs the Go Server assertion.
- **behavior-difference**: a documented server-zd difference. Binary mode
  asserts the server-zd behavior.
- **go-server-only**: the test covers Temporal, Cadence, push Workers, Go Server
  configuration, or Go Server internals. It goes away with the Go Server.
- **harness-gap**: the binary-mode harness skips behavior that server-zd
  supports. The row proposes the harness fix.

C numbers refer to the server-zd compatibility differences that the binary-mode
guards already cite.

## zd-bug

1. Fixed by #1011. **TestHardDeleteAttributeSubFlowTemporal** (high severity, #1001): a SubFlow
   started with a hard-delete Attribute in **SubFlowOptions.Attributes** stops
   the whole server node. Apply fails with
   `apply invariant violated: hard-delete write of "secret" has no uploaded object`,
   and the log ends with `dex-server-zd stopping after a failure`. The test now
   runs in binary mode and passes.
2. **TestLockingWorkflowTemporal** (low severity, #1002): an RPC that conflicts
   with a lock held by a running Step returns `ABORTED` with the expected
   sub-status, but its detail is
   `RPC lock conflict: attribute key CustomIntField is locked by a running Step`.
   The Go Server returns `one or more attribute keys are locked`
   (`locking_test.go:150`). The test now runs in binary mode, and only this
   detail check pins the server-zd text.
3. **TestPullSubFlowRejectsInvalidTargetTemporal** (medium severity, #1009): a pull
   Worker WaitFor that starts a SubFlow whose **WorkerTarget** sets an address
   fails the parent Flow as `FLOW_ERROR_TYPE_INTERNAL` with
   `server-zd does not support Worker target addresses`. The Go Server fails it
   as `FLOW_ERROR_TYPE_INVALID_USER_FLOW_CODE` with
   `SubFlow start rejected: pull worker_target cannot specify address`. The
   error comes from Worker code, so INTERNAL misleads the application.
4. Fixed by #1037. **TestPullWorkerUnavailableTemporal** (low severity, #1008): when no Worker accepts a
   WaitFor task, the **StepWaitForFailed** event has
   **DispatchTaskErrorReason** `UNSPECIFIED`, and its detail is
   `DISPATCH_TASK_ERROR_REASON_NO_WORKER_AVAILABLE: no Worker accepted the task`.
   The Go Server sets the reason field and the detail
   `no Worker available for queue DispatchProbeFlow: no Worker accepted the task`.
   The Flow error message already matches. The guard is deleted, and binary
   mode asserts the Go Server reason and detail.
5. Fixed by #1038. **TestSynchronousUpdateRequestIDTemporal** (medium severity, #1005): two concurrent
   transactional **InvokeRPC** calls with the same request ID can both reach the
   Worker. Three local runs invoked the Worker twice. A later retry with that
   request ID replays the recorded response. A client that retries after its own
   timeout while the first call still runs can run the RPC twice. The guard is
   deleted, and binary mode expects one Worker invocation.
6. Fixed by #1037. **TestTimerFlowTemporal** and
   **TestGreedyTimerFlowTemporal** (low severity, #1007): **GetFlowState** reports a timer skipped through **SkipTimer** as
   `INTERNAL_TIMER_STATUS_FIRED`, never `INTERNAL_TIMER_STATUS_SKIPPED`
   (`dexzd-apply/src/read_model/flow.rs` maps every completed timer to fired).
   The guard is deleted, and binary mode expects a skipped status.
7. Fixed by #1019. **TestFlowTimeoutTemporal** (medium severity, #1006): a retry of the Flow timeout
   handler reloads its selected Channels. A message published between the
   attempts appears in the second attempt. The Step options documentation says a
   retry reuses the state snapshot loaded for its first attempt
   (`docs/content/primitives/step/step-options.mdx`). The guard is deleted, and
   binary mode expects the retry to see only the first message.

Candidates from the forced **TestWebAPITemporal** run are tracked in #1003.

## harness-gap

Fixed in this change:

- **Temporal-only assertions inside product scenarios** (13 tests): the timer,
  greedy timer, signal, SubFlow condition, WaitForAttribute, search attribute,
  Attribute index, Step lineage, wait-for-Step-completion, read-behind, and
  command-combination scenarios now guard only their Temporal queries, Temporal
  history, Temporal Update counts, and Go Blob Store counts. Binary mode reads the
  same state through **GetFlowState**, **GetFlowSummary**,
  **GetChannelMessages**, and **GetHistoryEvents** where server-zd exposes it.
  WaitForAttribute and WaitForStepCompletion skip their Update handler, Update
  counter, and Continue-As-New scenarios in binary mode. Binary mode cannot
  observe when a waiter registers, so those waits rely on server-zd checking the
  current value on arrival.
- **TestChannelMultivalueTemporal** reads unconsumed messages through
  **GetChannelMessages**.
- **TestFlowTimeoutTemporal** guards the Go Server terminal event listener, the
  timeout Step timer query, and the Continue-As-New scenarios.
- **S3TestThreshold** (10 tests) maps to **values.wireInlineThresholdBytes**.
  The Go Server offloads a string or object whose payload is longer than the
  threshold. server-zd offloads a string or object Value whose protobuf encoded
  length is longer than its threshold. The harness sets the server-zd threshold
  to the encoded length of a `json` object Value whose payload has exactly
  S3TestThreshold bytes. Both servers then offload the same `json` objects. For
  payloads under 120 bytes the encoded length is the payload plus 10 bytes.
  A string Value under 128 bytes costs the payload plus 2 bytes. So, for
  thresholds under 120 bytes, strings up to 8 bytes over the threshold stay
  inline on server-zd. Larger thresholds widen this gap by a few bytes. No
  ported test uses such a string.
  With lazy loading, the S3 Start input, Step input, initial Attribute, and
  upserted Attribute tests assert that server-zd sends each large value to the
  Worker as a reference and each small value inline. The get/set Attribute,
  selected Channel, and blob-backed WaitForAttribute tests already observe the
  reference. RPC input stays inline on server-zd by design (C5). The
  **LocalBlobThreshold** setting uses the same mapping. Go Blob Store object
  counts and Temporal history checks run only against the Go Server.

Open:

- **TestWebAPITemporal**: tracked in #1003.

## behavior-difference

- **TestStartDelayTemporal**: server-zd rejects `flow_start_delay_seconds > 0`
  with `InvalidArgument` (C8). Binary mode asserts that error.
- **TestStepStateLoadingTemporal**: the scenario ordered its Steps with a start
  delay (C8). Binary mode starts a gate Step that waits for a Channel instead.
- **TestHardDeleteAttributesTemporal**: server-zd stores hard-delete objects in
  its object store (C4, C5). Binary mode skips the Go Blob directory checks and
  asserts that **LoadBlobs** rejects a Go Blob ID with `InvalidArgument`. It
  checks that Dex history events never contain a hard-delete value.
- **TestMongoDBAttributeSyncTemporal**: server-zd supports only PostgreSQL
  Attribute Stores (C26). The guarded skip stays until server-zd adds MongoDB.
- **TestPullSameWorkerFallbackTemporal**: server-zd records
  `PULL_ROUTING_SOURCE_STICKY_QUEUE` for every task its sticky Worker serves,
  even one that arrives on the Flow type stream.
- **TestSubFlowConditionTemporal**: a SubFlow request ID is
  `<run ID>:<run attempt>:<Step execution ID>:<condition index>`. The Go Server
  uses `<run ID><Step execution ID>`. A reset increments the run attempt.
- **TestTimerFlowTemporal**, **TestGreedyTimerFlowTemporal**, and
  **TestLockingWorkflowTemporal**: Temporal reset
  reapplies the **SkipTimer** and RPC writes made after the reset point. A
  server-zd reset discards them. Binary mode skips the timers again after the
  reset and expects no RPC reapply.
- **TestFlowTimeoutTemporal**: server-zd rejects **FlowStartOptions.RetryPolicy**
  with `server-zd does not support Flow retry_policy`. It does not list the Flow
  timeout as a `sys:timeout_handler` Step timer before the timeout fires.
- **TestRpcExternalStorageLockingTemporal** and
  **TestRpcExternalStorageNonLockingTemporal**: server-zd passes RPC input to
  the Worker inline, returns RPC output inline, and returns large values inline
  in history events (C5). It records **RpcExecutionCompleted** for every RPC
  with effects, including non-transactional RPCs.
- **TestSynchronousUpdateRequestIDTemporal**: the Go Server deduplicates every
  RPC by request ID only when it delivers RPCs as Temporal Updates. server-zd
  deduplicates transactional RPCs, so binary mode sends transactional RPCs.
- **Step heartbeat timeout**: server-zd requires at least 10 seconds. The pull
  heartbeat and Step cancellation scenarios use 10 seconds in binary mode.

## Ported go-server-only behaviors

These tests start the Server through **startPullDispatchService**, which
launches `dex-server-zd` in binary mode and passes the matching waits to it.
The test names are unchanged, so Go Server coverage stays until the Go Server is
removed.

| Behavior | Test |
| --- | --- |
| Worker heartbeat timeout, checkpoint restore, periodic heartbeats, unassigned wait | TestPullWorkerHeartbeatTimeoutTemporal |
| Worker disconnect during WaitFor redispatches the task | TestPullWorkerDisconnectTemporal |
| Worker that appears during retries completes the Flow | TestPullWorkerAppearsDuringRetriesTemporal |
| Unknown Step or RPC name redispatches; bounded failure | TestPullUnknownNameRedispatchTemporal, TestPullUnknownRPCBoundedFailureTemporal |
| Transactional RPC short budget and timeout releasing its lock | TestPullTransactionalRPCShortBudgetTemporal, TestPullTransactionalRPCTimeoutReleasesLockTemporal |
| RPC dispatch respects the caller deadline | TestPullRPCDispatchRespectsCallerDeadlineTemporal |
| StopFlow sends CancelTask to a held WaitFor | TestPullWorkerCancellationTemporal |
| SubFlow and StartFlow Worker target validation | TestPullSubFlowRejectsInvalidTargetTemporal, TestPullWorkerTargetValidationTemporal |
| Error text parity, pull versus push | TestPullWorkerErrorMatchesPushTemporal, TestPullStepErrorMatchesPushTemporal, TestPullDispatchFailureTextTemporal, TestPullWorkerUnavailableTemporal |
| Pull routing history, sticky fallback, capacity recovery, parallel last write | TestPullRoutingTemporal, TestPullSameWorkerFallbackTemporal, TestPullRPCRoutingTemporal, TestPullRPCSignalRoutingTemporal, TestPullWorkerStickyFallbackTemporal, TestPullWorkerStickyCapacityRecoveryTemporal, TestPullParallelLastWriteTemporal |
| Pull dispatch with progress Streams; SubFlow from a pull Worker | TestPullWorkerDispatchTemporal, TestPullSubFlowStartsWithoutStickyQueueTemporal |
| Terminal producer drain and RPC request ID deduplication without Temporal Updates | TestTerminalProducerDrainTemporal, TestSynchronousUpdateRequestIDTemporal |

Binary mode compares pull results with the push results that the Go Server
returns, because server-zd has no push Workers (C18). It replaces the Temporal
task queue query with the sticky Worker from **GetFlowState**, and the Go wake
hook with the active Step's last failure.

## go-server-only

| Reason | Tests |
| --- | --- |
| Cadence backend | 125 |
| Temporal client, Update, Signal, memo, metrics, direct workflow start, and Temporal queries | 13 |
| Continue-As-New (C3) | 12 |
| Go Server pull matching, membership, deployment roles, and wake hook | 8 |
| Go Server Blob Store, Blob cache, and async snapshot settings (C2, C5) | 8 |
| Push Workers (C18) and push Worker headers | 4 |
| Temporal local-activity phase of ASYNC Steps (C2) | 3 |
| Go Server package default Flow configuration | 2 |
| Go Server Redis Stream Store backend | 1 |

The first baseline listed 34 tests under Go Server pull matching and dispatch
internals. This change ports 24 of them. The remaining rows cite the server-zd
test that covers the same behavior where one exists.

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
| TestAnyCommandCombinationFlowTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
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
| TestAttributeIndexSyncTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
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
| TestBinaryTemporalOnlyScenarioSkipsBeforePolling | skip |  | Harness self-test; its subtest skip is the asserted behavior, so it has no classification. |
| TestChannelMessageQueueCadence | skip | go-server-only | Skip reason: `channel_message_queue_test.go:48: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestChannelMessageQueueTemporal | pass |  |  |
| TestChannelMultivalueCadence | skip | go-server-only | Skip reason: `channel_multivalue_test.go:60: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestChannelMultivalueTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
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
| TestDeadEndFlowTemporalContinueAsNew | skip | go-server-only | Skip reason: `deadend_test.go:200: Temporal-only assertion`. Continue-As-New variant; server-zd never continues a run as new (C3). |
| TestFlowForceFailCadence | skip | go-server-only | Skip reason: `wf_force_fail_test.go:39: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestFlowForceFailTemporal | pass |  |  |
| TestFlowIDWithTenantPrefixPassesThroughTemporal | skip | go-server-only | Skip reason: `flow_id_passthrough_test.go:87: Temporal-only assertion`. Starts the interpreter workflow directly through Temporal (StartInterpreterWorkflow). |
| TestFlowTimeoutCadence | skip | go-server-only | Skip reason: `wf_timeout_test.go:72: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestFlowTimeoutTemporal | pass | behavior-difference | Harness gap fixed. Passes; asserts that server-zd rejects the Flow retry policy. zd-bug 7 (#1006) fixed by #1019, so the timeout handler retry runs the Go Server assertion. |
| TestFlowWaitForTimeoutCadence | skip | go-server-only | Skip reason: `get_with_wait_timeout_test.go:39: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestFlowWaitForTimeoutTemporal | pass |  |  |
| TestForceCompleteIfChannelsEmptyCountsCommittedConsumptionCadence | skip | go-server-only | Skip reason: `conditional_close_committed_consumption_test.go:83: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestForceCompleteIfChannelsEmptyCountsCommittedConsumptionTemporal | pass |  |  |
| TestGeneralAuthenticationCadence | skip | go-server-only | Skip reason: `general_auth_test.go:47: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestGeneralAuthenticationTemporal | pass |  |  |
| TestGeneralWakeCadence | skip | go-server-only | Skip reason: `general_wake_test.go:41: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestGeneralWakeTemporal | skip | go-server-only | Go Server Worker wake hook; server-zd has no wake hook. Pull redelivery is covered by TestPullWorkerAppearsDuringRetriesTemporal. |
| TestGetHistoryEventsNotFoundCadence | skip | go-server-only | Skip reason: `history_not_found_test.go:34: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestGetHistoryEventsNotFoundTemporal | pass |  |  |
| TestGreedyAnyTimerSignalFlowCadence | skip | go-server-only | Skip reason: `any_timer_signal_test.go:58: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestGreedyAnyTimerSignalFlowCadenceContinueAsNew | skip | go-server-only | Skip reason: `any_timer_signal_test.go:106: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestGreedyAnyTimerSignalFlowTemporal | pass |  |  |
| TestGreedyAnyTimerSignalFlowTemporalContinueAsNew | pass |  |  |
| TestGreedyTimerFlowBaseCadence | skip | go-server-only | Skip reason: `greedy_timer_test.go:41: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestGreedyTimerFlowBaseCadenceContinueAsNew | skip | go-server-only | Skip reason: `greedy_timer_test.go:61: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestGreedyTimerFlowBaseTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestGreedyTimerFlowBaseTemporalContinueAsNew | skip | go-server-only | Skip reason: `greedy_timer_test.go:81: Temporal-only assertion`. Continue-As-New variant; server-zd never continues a run as new (C3). |
| TestGreedyTimerFlowCadence | skip | go-server-only | Skip reason: `timer_test.go:82: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestGreedyTimerFlowCadenceContinueAsNew | skip | go-server-only | Skip reason: `timer_test.go:102: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestGreedyTimerFlowTemporal | pass | behavior-difference | Harness gap fixed. Passes; asserts the reset difference. zd-bug 6 (#1007) fixed by #1037, so the skipped timer status runs the Go Server assertion. |
| TestGreedyTimerFlowTemporalContinueAsNew | skip | go-server-only | Skip reason: `timer_test.go:117: Temporal-only assertion`. Continue-As-New variant; server-zd never continues a run as new (C3). |
| TestHardDeleteAttributeBlobCacheTemporal | skip | go-server-only | Skip reason: `hard_delete_attribute_test.go:462: Temporal-only assertion`. Go Server Blob cache directory with S3 (C5). |
| TestHardDeleteAttributeCleanupTemporal | skip | go-server-only | Skip reason: `hard_delete_attribute_test.go:505: Temporal-only assertion`. Go Server hard-delete object cleanup in its local Blob directory; a forced run panics on a nil Go Blob Store. |
| TestHardDeleteAttributeRequiresBlobStoreTemporal | skip | go-server-only | Skip reason: `hard_delete_attribute_test.go:398: server-zd does not support disabled Blob Store`. Go Server Blob Store switch; server-zd always stores values in its log (C5). |
| TestHardDeleteAttributeResetCadence | skip | go-server-only | Skip reason: `hard_delete_attribute_test.go:248: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestHardDeleteAttributeResetTemporal | pass |  |  |
| TestHardDeleteAttributeStepWriteRequiresBlobStoreTemporal | skip | go-server-only | Skip reason: `hard_delete_attribute_test.go:422: Temporal-only assertion`. Go Server disabled Blob Store rejection (C5). |
| TestHardDeleteAttributeSubFlowCadence | skip | go-server-only | Skip reason: `hard_delete_attribute_test.go:306: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestHardDeleteAttributeSubFlowTemporal | pass |  | zd-bug 1 (#1001) fixed by #1011; runs against dex-server-zd. |
| TestHardDeleteAttributeValidationTemporal | pass |  |  |
| TestHardDeleteAttributesCadence | skip | go-server-only | Skip reason: `hard_delete_attribute_test.go:69: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestHardDeleteAttributesTemporal | pass | behavior-difference | Passes; skips the Go Blob directory checks (C4, C5) and asserts that LoadBlobs rejects a Go Blob ID. Dex history is checked for secrets. |
| TestHeadersFlowCadence | skip | go-server-only | Skip reason: `headers_test.go:53: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestHeadersFlowTemporal | skip | go-server-only | Skip reason: `headers_test.go:74: server-zd does not support default Worker headers`. Go Server headers sent to push Workers. |
| TestIgnoreAlreadyStartedFlowCadence | skip | go-server-only | Skip reason: `wf_ignore_already_started_test.go:49: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestIgnoreAlreadyStartedFlowTemporal | pass |  |  |
| TestInterStateWorkflowCadence | skip | go-server-only | Skip reason: `internalchannel_test.go:45: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestInterStateWorkflowTemporal | pass |  |  |
| TestInvokeRPCReadOnlyQueryAfterTerminalTemporal | pass |  |  |
| TestInvokeRPCSignalRejectedAfterTerminalTemporal | pass |  |  |
| TestInvokeRPCUpdateRejectedAfterTerminalTemporal | skip | go-server-only | Temporal Update delivery. Covered on server-zd by TestInvokeRPCSignalRejectedAfterTerminalTemporal and server-zd/crates/dexzd-e2e-tests/tests/dev_server/rpc_closed_run.rs. |
| TestLargeDataAttributesTemporalContinueAsNew | skip | go-server-only | Skip reason: `all 2 subtests skipped: large_data_attributes_test.go:47: Temporal-only assertion`. Continue-As-New variant; server-zd never continues a run as new (C3). |
| TestLocalStepCancellationCadence | skip | go-server-only | Skip reason: `step_cancellation_test.go:65: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestLocalStepCancellationTemporal | pass |  |  |
| TestLocalTimeoutFallbackCancellationCadence | skip | go-server-only | Skip reason: `step_cancellation_test.go:81: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestLocalTimeoutFallbackCancellationTemporal | skip | go-server-only | Skip reason: `step_cancellation_test.go:75: server-zd does not support ASYNC local phase`. Temporal local-activity phase of ASYNC Steps; server-zd has none (C2). |
| TestLockingWorkflowCadence | skip | go-server-only | Skip reason: `locking_test.go:56: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestLockingWorkflowCadenceContinueAsNew | skip | go-server-only | Skip reason: `locking_test.go:66: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestLockingWorkflowTemporal | pass | zd-bug | Harness gap fixed. Passes; its lock-conflict detail check pins zd-bug 2 (#1002), and it skips the RPC reapply after reset. |
| TestLockingWorkflowTemporalContinueAsNew | skip | go-server-only | Skip reason: `locking_test.go:85: Temporal-only assertion`. Continue-As-New variant; server-zd never continues a run as new (C3). |
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
| TestPullAsyncWaitContinueAsNewReplayTemporal | skip | go-server-only | Temporal replay across Continue-As-New (C3). |
| TestPullDispatchFailureTextCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:899: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullDispatchFailureTextTemporal | pass | go-server-only | Ported from go-server-only. The recovery Step subtest passes; the SubFlow child subtest needs a push parent Worker (C18). |
| TestPullParallelLastWriteCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1141: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullParallelLastWriteTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullRPCDispatchRespectsCallerDeadlineCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1793: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullRPCDispatchRespectsCallerDeadlineTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullRPCRoutingTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullRPCSignalRoutingCadence | skip | go-server-only | Skip reason: `pull_routing_test.go:162: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullRPCSignalRoutingTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullRPCWhenMatchingClosesCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:652: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullRPCWhenMatchingClosesTemporal | skip | go-server-only | Closes the in-process Go matching engine. |
| TestPullRoutingCadence | skip | go-server-only | Skip reason: `pull_routing_test.go:55: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullRoutingReplayTemporal | skip | go-server-only | Temporal replay across Continue-As-New (C3). Routing is covered by TestPullRoutingTemporal. |
| TestPullRoutingTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullSameWorkerFallbackCadence | skip | go-server-only | Skip reason: `pull_routing_test.go:69: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullSameWorkerFallbackTemporal | pass | behavior-difference | Ported from go-server-only. Passes; server-zd labels a task its sticky Worker serves as STICKY_QUEUE whichever stream delivered it. |
| TestPullStepErrorMatchesPushCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1739: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullStepErrorMatchesPushTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullSubFlowRejectsInvalidTargetCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1102: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullSubFlowRejectsInvalidTargetTemporal | pass | zd-bug | Ported from go-server-only. Passes; its error check pins zd-bug 3 (#1009, invalid SubFlow target fails as INTERNAL). |
| TestPullSubFlowStartsWithoutStickyQueueCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1056: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullSubFlowStartsWithoutStickyQueueTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullTransactionalRPCShortBudgetTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullTransactionalRPCTimeoutReleasesLockTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullUnknownNameRedispatchCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1510: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullUnknownNameRedispatchTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullUnknownRPCBoundedFailureCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1524: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullUnknownRPCBoundedFailureTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWaitForAttributeSuccessTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestPullWorkerAllInOneCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:638: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerAllInOneTemporal | skip | go-server-only | Go Server service-role deployment; server-zd is one binary. |
| TestPullWorkerAppearsDuringRetriesCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1000: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerAppearsDuringRetriesTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerAsyncLocalCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1042: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerAsyncLocalTemporal | skip | go-server-only | Temporal local-activity phase of ASYNC Steps (C2). |
| TestPullWorkerCancellationCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:720: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerCancellationTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerDisconnectCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1014: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerDisconnectTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerDispatchCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:608: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerDispatchTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerErrorMatchesPushCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1725: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerErrorMatchesPushTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerHeartbeatTimeoutCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:734: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerHeartbeatTimeoutTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerQueueSurvivesContinueAsNewCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1294: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerQueueSurvivesContinueAsNewTemporal | skip | go-server-only | Continue-As-New (C3). |
| TestPullWorkerRoutingCadence | skip | go-server-only | Skip reason: `matching_poll_test.go:104: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerRoutingTemporal | skip | go-server-only | Go matching membership and internal DispatchTask API. |
| TestPullWorkerSplitRoleCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:623: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerSplitRoleTemporal | skip | go-server-only | Go Server split matching role and membership; server-zd is one binary. |
| TestPullWorkerStickyCapacityRecoveryCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1028: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerStickyCapacityRecoveryTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerStickyFallbackCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:986: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerStickyFallbackTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerTargetValidationCadence | skip | go-server-only | Skip reason: `pull_target_validation_test.go:33: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerTargetValidationTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerUnavailableCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1711: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPullWorkerUnavailableTemporal | pass |  | Ported from go-server-only. zd-bug 4 (#1008) fixed by #1037; runs the Go Server StepWaitForFailed assertion against dex-server-zd. |
| TestPushParentRoutesSubFlowToPullWorkerCadence | skip | go-server-only | Skip reason: `pull_worker_test.go:1070: if !*cadenceIntegTest \|\| *dexServerAddress != ""`. Cadence backend; server-zd has no Cadence. |
| TestPushParentRoutesSubFlowToPullWorkerTemporal | skip | go-server-only | Push parent Worker (C18). SubFlows from pull Workers are covered by TestPullSubFlowStartsWithoutStickyQueueTemporal. |
| TestQueuedStepCancellationAcrossContinueAsNewCadence | skip | go-server-only | Skip reason: `step_cancellation_test.go:51: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestQueuedStepCancellationAcrossContinueAsNewTemporal | skip | go-server-only | Skip reason: `step_cancellation_test.go:45: server-zd does not support Continue-As-New`. Continue-As-New; server-zd never continues a run as new (C3). |
| TestReadBehindSlowEngineTaskTemporal | pass | go-server-only | 1 of 3 subtests skipped: it times out Temporal queries behind a slow workflow task; server-zd reads do not queue behind Worker calls. |
| TestResetByStateIdWorkflowCadence | skip | go-server-only | Skip reason: `reset_by_state_id_test.go:39: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestResetByStateIdWorkflowTemporal | pass |  |  |
| TestRpcExternalStorageLockingTemporal | pass | behavior-difference | Harness gap fixed. Passes; server-zd returns RPC input and output inline and keeps values inline in history (C5). |
| TestRpcExternalStorageNonLockingCadence | skip | go-server-only | Skip reason: `rpc_external_storage_test.go:74: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestRpcExternalStorageNonLockingTemporal | pass | behavior-difference | Harness gap fixed. Passes; server-zd records RpcExecutionCompleted for plain RPCs with effects and keeps values inline in history (C5). |
| TestRpcExternalStorageSynchronousUpdateTemporal | skip | go-server-only | Temporal Update delivery. Covered on server-zd by TestRpcExternalStorageLockingTemporal and TestRpcExternalStorageNonLockingTemporal. |
| TestRpcInlineTransportCadence | skip | go-server-only | Skip reason: `rpc_inline_transport_test.go:152: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestRpcInlineTransportTemporal | skip | go-server-only | Skip reason: `all 8 subtests skipped: rpc_inline_transport_test.go:167: Temporal-only assertion`. Uses the Go Server async Step input snapshot setting (C2, C5). |
| TestRpcLockingErrorMappingTemporal | pass |  |  |
| TestRpcLockingUnimplementedCadence | skip | go-server-only | Skip reason: `rpc_test.go:112: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestRpcReadBlobStoreUnavailableCadence | skip | go-server-only | Skip reason: `rpc_inline_transport_test.go:83: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestRpcReadBlobStoreUnavailableTemporal | skip | go-server-only | Skip reason: `all 4 subtests skipped: rpc_inline_transport_test.go:100: Temporal-only assertion`. Uses the Go Server async Step input snapshot setting and Blob Store outages (C5). |
| TestRpcSelectedChannelExternalStorageCadence | skip | go-server-only | Skip reason: `rpc_external_storage_test.go:94: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestRpcSelectedChannelExternalStorageTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestRpcSignalWireCompatibilityCadence | skip | go-server-only | Skip reason: `rpc_inline_transport_test.go:320: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestRpcSignalWireCompatibilityTemporal | skip | go-server-only | Skip reason: `rpc_inline_transport_test.go:315: Temporal-only assertion`. Decodes Temporal Signal payloads. |
| TestRpcStepCancellationCadence | skip | go-server-only | Skip reason: `step_cancellation_test.go:102: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestRpcStepCancellationSynchronousUpdateTemporal | skip | go-server-only | Temporal Update delivery. Covered on server-zd by TestRpcStepCancellationTemporal. |
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
| TestS3GetSetDataAttributesTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestS3GetSetDataAttributesWithInitialDataTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestS3KeyPrefixLayoutAndCleanupTemporal | skip | go-server-only | Skip reason: `s3_prefix_test.go:32: Temporal-only assertion`. Go Server Blob Store key layout in MinIO (C5). |
| TestS3WorkflowInitDataAttributesCadence | skip | go-server-only | Skip reason: `s3_wf_init_data_attributes_test.go:44: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestS3WorkflowInitDataAttributesTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestS3WorkflowStartInputCadence | skip | go-server-only | Skip reason: `s3_wf_start_input_test.go:44: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestS3WorkflowStartInputTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestS3WorkflowStateInputOptimizationCadence | skip | go-server-only | Skip reason: `s3_wf_state_input_optimization_test.go:43: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestS3WorkflowStateInputOptimizationTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestS3WorkflowUpsertDataObjectsCadence | skip | go-server-only | Skip reason: `s3_wf_upsert_data_objects_test.go:44: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestS3WorkflowUpsertDataObjectsTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestSearchFlowsExcludeInternalWorkflowCadence | skip | go-server-only | Skip reason: `search_runs_test.go:40: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestSearchFlowsExcludeInternalWorkflowTemporal | skip | go-server-only | Skip reason: `search_runs_test.go:46: server-zd does not support internal Blob Store cleanup workflows`. Go Server Blob Store cleanup workflows (C5). |
| TestSearchRunsCadence | skip | go-server-only | Skip reason: `search_runs_test.go:129: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestSearchRunsTemporal | pass |  |  |
| TestServerInfoCadence | skip | go-server-only | Skip reason: `server_info_test.go:31: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestServerInfoTemporal | pass |  |  |
| TestSetDataAttributesTemporal | pass |  |  |
| TestSetSearchAttributes | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestSignalWorkflowCadence | skip | go-server-only | Skip reason: `signal_test.go:52: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestSignalWorkflowCadenceContinueAsNew | skip | go-server-only | Skip reason: `signal_test.go:62: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestSignalWorkflowTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestSignalWorkflowTemporalContinueAsNew | skip | go-server-only | Skip reason: `signal_test.go:79: Temporal-only assertion`. Continue-As-New variant; server-zd never continues a run as new (C3). |
| TestSkipStartFlowCadence | skip | go-server-only | Skip reason: `skip_start_test.go:48: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestSkipStartFlowCadenceContinueAsNew | skip | go-server-only | Skip reason: `skip_start_test.go:58: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestSkipStartFlowTemporal | pass |  |  |
| TestSkipStartFlowTemporalContinueAsNew | pass |  |  |
| TestStartDelayCadence | skip | go-server-only | Skip reason: `start_delay_test.go:64: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestStartDelayTemporal | pass | behavior-difference | Passes; asserts that server-zd rejects flow_start_delay_seconds > 0 with InvalidArgument (C8). |
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
| TestStepLineageTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestStepStateLoadingCadence | skip | go-server-only | Skip reason: `step_state_loading_test.go:155: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestStepStateLoadingTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
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
| TestSubFlowConditionTemporal | pass | behavior-difference | Harness gap fixed. Passes; server-zd SubFlow request IDs add the run attempt and condition index. |
| TestSynchronousUpdateRequestIDTemporal | pass | behavior-difference | Ported from go-server-only with transactional RPCs. zd-bug 5 (#1005) fixed by #1038, so binary mode expects one Worker invocation. |
| TestTemporalAuthAPIKeyFileStartupValidation | pass |  |  |
| TestTemporalAuthClientCertificateRotation | skip | go-server-only | Skip reason: `temporal_auth_test.go:117: server-zd does not support Temporal client authentication`. Temporal client TLS and API key settings. |
| TestTemporalAuthCustomCAAndServerName | skip | go-server-only | Skip reason: `temporal_auth_test.go:97: server-zd does not support Temporal client authentication`. Temporal client TLS and API key settings. |
| TestTemporalAuthPlaintextKeyCompatibility | skip | go-server-only | Skip reason: `temporal_auth_test.go:148: server-zd does not support Temporal client authentication`. Temporal client TLS and API key settings. |
| TestTemporalAuthTokenRotation | skip | go-server-only | Skip reason: `temporal_auth_test.go:38: server-zd does not support Temporal client authentication`. Temporal client TLS and API key settings. |
| TestTemporalMetrics | skip | go-server-only | Skip reason: `metrics_test.go:49: server-zd does not support Temporal metrics handler`. Temporal SDK metrics. |
| TestTerminalProducerDrainCadence | skip | go-server-only | Skip reason: `terminal_producer_drain_test.go:111: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestTerminalProducerDrainTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestTimerFlowCadence | skip | go-server-only | Skip reason: `timer_test.go:41: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestTimerFlowCadenceContinueAsNew | skip | go-server-only | Skip reason: `timer_test.go:61: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestTimerFlowTemporal | pass | behavior-difference | Harness gap fixed. Passes; asserts the reset difference. zd-bug 6 (#1007) fixed by #1037, so the skipped timer status runs the Go Server assertion. |
| TestTimerFlowTemporalContinueAsNew | skip | go-server-only | Skip reason: `timer_test.go:117: Temporal-only assertion`. Continue-As-New variant; server-zd never continues a run as new (C3). |
| TestUpdateWorkerTargetCadence | skip | go-server-only | Skip reason: `worker_target_update_test.go:35: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestUpdateWorkerTargetTemporal | skip | go-server-only | Skip reason: `worker_target_update_test.go:41: requires push Workers: switching addresses between Workers for one Flow type`. Push Worker address switching; server-zd supports only pull Workers (C18). |
| TestWaitForAttributeBlobBackedTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestWaitForAttributeCadence | skip | go-server-only | Skip reason: `wait_for_attribute_test.go:86: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestWaitForAttributeTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestWaitForStateCompletionCadence | skip | go-server-only | Skip reason: `wait_for_state_completion_test.go:71: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestWaitForStateCompletionTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestWaitUntilSearchAttributesOptimizationWorkflowTemporal | skip | go-server-only | Skip reason: `wait_until_search_attributes_optimization_test.go:72: Temporal-only assertion`. Exercises the Go Server package default Flow configuration. |
| TestWaitUntilSearchAttributesWorkflowTemporal | skip | go-server-only | Skip reason: `wait_until_search_attributes_test.go:67: server-zd does not support built-in default Flow configuration`. Exercises the Go Server package default Flow configuration. |
| TestWebAPICadence | skip | go-server-only | Skip reason: `web_api_test.go:48: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestWebAPITemporal | skip | harness-gap | Skip reason: `all 26 subtests skipped: web_api_test.go:1483: Temporal-only assertion`. Whole test skipped in binary mode. A forced run: 2 subtests pass, 14 skip, 11 fail. Failures mix Temporal-only calls (DescribeWorkflowExecution), Go async snapshot settings (C2), and history differences listed under zd-bug candidates. Proposed fix: split the subtests and guard the Temporal-only parts. |
| TestWfStateOptionsDataAttributesLoading | pass |  |  |
| TestWfStateOptionsSearchAttributesLoading | pass |  |  |
| TestWorkflowCanceledCadence | skip | go-server-only | Skip reason: `wf_stop_test.go:102: if !*cadenceIntegTest`. Cadence backend; server-zd has no Cadence. |
| TestWorkflowCanceledTemporal | pass |  |  |
