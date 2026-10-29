# server-zd integration tests

This standalone Go module exercises dex-server-zd through its public gRPC APIs
and pull Worker protocol. Each scenario launches an isolated dev process with
its own data directory and ephemeral listener. Cleanup stops and reaps that
process, including when a test fails.

The module path is `github.com/superdurable/dex/server-zd/integ`. It imports
protocol types from `sdk-go/gen/dexpb` and pointer helpers from `sdk-go/dex/ptr`.
Its module-local replace points to the sibling SDK checkout, so `GOWORK=off`
uses the checked-in protocol version without depending on the Go Server module.
`internal/testconfig` contains only the configuration shapes this harness writes.
The full original dependency inventory is in [DEPENDENCY_AUDIT.md](DEPENDENCY_AUDIT.md).

## Running

Build from the repository root, then run the full suite:

```bash
cargo build --locked --manifest-path server-zd/Cargo.toml -p dexzd-server --bin dex-server-zd
make -C server-zd/integ compile
make -C server-zd/integ zdIntegTests totalPartitions=5 partitionNum=0 \
  dexServerZDBinary=../target/debug/dex-server-zd \
  zdIntegTestJSON=/tmp/test-zd-integ-0.json 2>&1 | tee /tmp/test-zd-integ-0.log
```

Run every partition, from 0 through 4. Omitting partition arguments runs all tests
in one partition. The runner uses `GOWORK=off` and enables the
`attributestore_integration` build tag for both test discovery and execution.
It writes JSON events and the selected test names, then runs the summarizer.
A failed process, missing test event, or failed test makes the target fail.
An optional `testAllowlist=/absolute/path/tests.txt` selects named tests before
partitioning and validates that every listed test exists.

For local runs without PostgreSQL, create an allowlist from the untagged suite
and pass it to the same runner:

```bash
cd server-zd/integ
GOWORK=off go test . -list '^Test' | sed -n '/^Test/p' > /tmp/zd-local-tests.txt
make zdIntegTests testAllowlist=/tmp/zd-local-tests.txt \
  zdIntegTestJSON=/tmp/test-zd-integ-local.json 2>&1 | tee /tmp/test-zd-integ-local.log
```

CI provides PostgreSQL 17 on port 55432 for these six tests:

- TestAttributeSyncTemporal
- TestAttributeSyncFlowTimeoutTemporal
- TestAttributeSyncInvokeRPCGracefulCompleteTemporal
- TestAttributeSyncRetryExhaustionTemporal
- TestAttributeSyncMapTablesTemporal
- TestAttributeSyncRowKeysTemporal

Set `DEX_ATTRIBUTE_STORE_POSTGRES_DSN` to override their loopback DSN. Do not
start dependency containers for a local run. The historical S3-named tests
exercise server-zd's embedded dev object store and wire-value references;
they do not require MinIO or external S3. Redis package tests were not copied.

## Extraction scope

The source baseline has 326 top-level tests. This module copies 136 names:
134 passing rows and the two applicable unsupported/harness-gap rows. The
original `server/integ` tree remains unchanged until the Go Server is deleted.
Temporal suffixes and some ContinueAsNew variant names remain for continuity;
these scenarios use only Dex APIs here and do not run backend-only assertions.

The other 190 names stay behind: 174 skipped Go Server tests, 13 direct Go
Stream Store package tests, the Temporal authentication startup validation,
and two obsolete binary-harness tests (unsupported Go settings and
Temporal-only scenario skipping). Passing rows with backend-only subtests keep
their applicable application assertions here. The pull error comparisons now check the recorded expected push error directly.

Small local helpers retain the protocol's reserved Step sources, timeout Step
identifiers, and search Attribute names. Logging uses standard JSON encoding.
No server bootstrap, UnifiedClient, Temporal/Cadence client, Go Blob Store,
matching Engine, or Go Stream Store implementation is imported or started.

## Inherited baseline

The source baseline was recorded by
[CI run 40784846162](https://github.com/superdurable/dex/actions/runs/40784846162)
on `ef640fbe2` and refreshed for fixes #1011, #1019, #1037, and #1038.
The table below includes only copied tests. Its evidence describes that baseline;
local extraction validation is recorded separately below.

| Result | Tests |
| --- | ---: |
| pass | 134 |
| skip | 2 |
| fail | 0 |
| total | 136 |

Classifications apply only to copied tests:

- **zd-bug**: TestLockingWorkflowTemporal pins the #1002 lock-conflict detail;
  TestPullSubFlowRejectsInvalidTargetTemporal pins the #1009 error type and
  detail. Both guards remain and fail if the behavior changes.
- **behavior-difference**: delayed starts and Flow retry policies are rejected;
  reset discards later writes; SubFlow request IDs contain run attempts;
  RPC values and history stay inline; sticky routing reports the serving queue;
  hard-delete objects cannot be loaded using Go Blob IDs. MongoDB Attribute
  synchronization keeps its existing C26 skip.
- **harness-gap**: applicable TestWebAPITemporal scenarios retain their existing
  #1003 guard. Resolving the history/state harness gap is separate work.

The value threshold is the encoded protobuf size of a JSON object with the
configured payload length. This preserves the copied tests' object boundaries.
For payloads under 120 bytes, that encoding adds 10 bytes; string Values under
128 bytes add 2 bytes, so their reference boundaries differ by up to 8 bytes.
The copied scenarios do not rely on that gap. Lazy-loading tests observe both
inline values and references through Worker requests and LoadBlobs.

Relative timer expectations use the persisted WaitFor completion timestamp.
server-zd rounds firing times up to seconds; measuring from the client before
Worker registration can exceed the existing one-second tolerance under load.
The timer duration and timestamp tolerance remain unchanged.

The fixed defects remain covered by application assertions: hard-delete
SubFlows (#1001/#1011), unavailable-Worker failure reasons (#1008/#1037),
concurrent transactional RPC request IDs (#1005/#1038), skipped timer state
(#1007/#1037), and timeout-handler retry snapshots (#1006/#1019).

## Validation

Local validation on 2026-10-28 used a debug dex-server-zd built from main
`47cd59290c71100bcc3797da6bd255135e5818b1`:

- All 130 tests without PostgreSQL dependencies ran across five partitions:
  **128 pass, 2 skip, 0 fail**. The skips are the existing #1003 Web API guard
  and C26 MongoDB guard.
- The six PostgreSQL synchronization tests compiled with
  `attributestore_integration`; their execution remains covered by CI.
- `make compile` passed with `GOWORK=off`, with and without that build tag.
- All five production partitions selected exactly 136 distinct expected names.
- Four summarizer regression tests, actionlint, and `make copyright-check`
  passed. The dependency graph contains no Go Server module, Temporal, or Cadence.
- Both timer scenarios passed after anchoring relative deadlines to history;
  the final full run includes this change. No dependency containers were started.

## Per-test inherited results

| Test | Result | Classification | Reason and evidence |
| --- | --- | --- | --- |
| TestAnyCommandCloseFlowTemporal | pass |  | |
| TestAnyCommandCloseFlowTemporalContinueAsNew | pass |  | |
| TestAnyCommandCombinationFlowTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestAnyCommandCompletedTemporal | pass |  | |
| TestAnyTimerSignalFlowTemporal | pass |  | |
| TestAnyTimerSignalFlowTemporalContinueAsNew | pass |  | |
| TestAttributeIndexSyncTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestAttributeSyncFlowTimeoutTemporal | pass |  | |
| TestAttributeSyncInvokeRPCGracefulCompleteTemporal | pass |  | |
| TestAttributeSyncMapTablesTemporal | pass |  | |
| TestAttributeSyncRetryExhaustionTemporal | pass |  | |
| TestAttributeSyncRowKeysTemporal | pass |  | |
| TestAttributeSyncTemporal | pass |  | |
| TestBasicFlowTemporal | pass |  | |
| TestBinaryDexConfigAttributeStoreMapping | pass |  | |
| TestBinaryDexConfigMapping | pass |  | |
| TestBinaryDexLauncher | pass |  | |
| TestBinaryDexLauncherDisablesStreamsByDefault | pass |  | |
| TestBinaryDexLauncherEarlyExit | pass |  | |
| TestBinaryDexLauncherKillsUnresponsiveProcess | pass |  | |
| TestBinaryDexLauncherReadinessTimeout | pass |  | |
| TestChannelMessageQueueTemporal | pass |  | |
| TestChannelMultivalueTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestCommandThreadCompletionTemporal | pass |  | |
| TestConditionalForceCompleteOnInternalChannelEmptyWorkflowTemporal | pass |  | |
| TestConditionalForceCompleteOnInternalChannelEmptyWorkflowTemporalContinueAsNew | pass |  | |
| TestCreateFlowTemporal | pass |  | |
| TestCreateFlowTemporalContinueAsNew | pass |  | |
| TestDeadEndFlowTemporal | pass |  | |
| TestFlowForceFailTemporal | pass |  | |
| TestFlowTimeoutTemporal | pass | behavior-difference | Harness gap fixed. Passes; asserts that server-zd rejects the Flow retry policy. zd-bug 7 (#1006) fixed by #1019, so the timeout handler retry runs the Go Server assertion. |
| TestFlowWaitForTimeoutTemporal | pass |  | |
| TestForceCompleteIfChannelsEmptyCountsCommittedConsumptionTemporal | pass |  | |
| TestGeneralAuthenticationTemporal | pass |  | |
| TestGetHistoryEventsNotFoundTemporal | pass |  | |
| TestGreedyAnyTimerSignalFlowTemporal | pass |  | |
| TestGreedyAnyTimerSignalFlowTemporalContinueAsNew | pass |  | |
| TestGreedyTimerFlowBaseTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestGreedyTimerFlowTemporal | pass | behavior-difference | Harness gap fixed. Passes; asserts the reset difference. zd-bug 6 (#1007) fixed by #1037, so the skipped timer status runs the Go Server assertion. |
| TestHardDeleteAttributeResetTemporal | pass |  | |
| TestHardDeleteAttributeSubFlowTemporal | pass |  | zd-bug 1 (#1001) fixed by #1011; runs against dex-server-zd. |
| TestHardDeleteAttributeValidationTemporal | pass |  | |
| TestHardDeleteAttributesTemporal | pass | behavior-difference | Passes; skips the Go Blob directory checks (C4, C5) and asserts that LoadBlobs rejects a Go Blob ID. Dex history is checked for secrets. |
| TestIgnoreAlreadyStartedFlowTemporal | pass |  | |
| TestInterStateWorkflowTemporal | pass |  | |
| TestInvokeRPCReadOnlyQueryAfterTerminalTemporal | pass |  | |
| TestInvokeRPCSignalRejectedAfterTerminalTemporal | pass |  | |
| TestLocalStepCancellationTemporal | pass |  | |
| TestLockingWorkflowTemporal | pass | zd-bug | Harness gap fixed. Passes; its lock-conflict detail check pins zd-bug 2 (#1002), and it skips the RPC reapply after reset. |
| TestMongoDBAttributeSyncTemporal | skip | behavior-difference | Existing C26 skip retained: server-zd supports PostgreSQL Attribute Stores only. |
| TestParallelFlowTemporal | pass |  | |
| TestPersistenceLoadingPolicy | pass |  | |
| TestPersistenceWorkflowTemporal | pass |  | |
| TestPersistenceWorkflowTemporalContinueAsNew | pass |  | |
| TestPublishToChannelNoFlowId | pass |  | |
| TestPullAdapterBasicTemporal | pass |  | |
| TestPullAdapterReconnects | pass |  | |
| TestPullAdapterRegistrationTimeout | pass |  | |
| TestPullAdapterRejectsAmbiguousWorkersTemporal | pass |  | |
| TestPullAdapterUnregisteredSubFlowTemporal | pass |  | |
| TestPullDispatchFailureTextTemporal | pass |  | Applicable application scenarios run; backend-only subtests stay in server/integ. |
| TestPullParallelLastWriteTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullRPCDispatchRespectsCallerDeadlineTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullRPCRoutingTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullRPCSignalRoutingTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullRoutingTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullSameWorkerFallbackTemporal | pass | behavior-difference | Ported from go-server-only. Passes; server-zd labels a task its sticky Worker serves as STICKY_QUEUE whichever stream delivered it. |
| TestPullStepErrorMatchesPushTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullSubFlowRejectsInvalidTargetTemporal | pass | zd-bug | Ported from go-server-only. Passes; its error check pins zd-bug 3 (#1009, invalid SubFlow target fails as INTERNAL). |
| TestPullSubFlowStartsWithoutStickyQueueTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullTransactionalRPCShortBudgetTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullTransactionalRPCTimeoutReleasesLockTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullUnknownNameRedispatchTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullUnknownRPCBoundedFailureTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWaitForAttributeSuccessTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestPullWorkerAppearsDuringRetriesTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerCancellationTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerDisconnectTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerDispatchTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerErrorMatchesPushTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerHeartbeatTimeoutTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerStickyCapacityRecoveryTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerStickyFallbackTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerTargetValidationTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestPullWorkerUnavailableTemporal | pass |  | Ported from go-server-only. zd-bug 4 (#1008) fixed by #1037; runs the Go Server StepWaitForFailed assertion against dex-server-zd. |
| TestReadBehindSlowEngineTaskTemporal | pass |  | Applicable application scenarios run; backend-only subtests stay in server/integ. |
| TestResetByStateIdWorkflowTemporal | pass |  | |
| TestRpcExternalStorageLockingTemporal | pass | behavior-difference | Harness gap fixed. Passes; server-zd returns RPC input and output inline and keeps values inline in history (C5). |
| TestRpcExternalStorageNonLockingTemporal | pass | behavior-difference | Harness gap fixed. Passes; server-zd records RpcExecutionCompleted for plain RPCs with effects and keeps values inline in history (C5). |
| TestRpcLockingErrorMappingTemporal | pass |  | |
| TestRpcSelectedChannelExternalStorageTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestRpcStepCancellationTemporal | pass |  | |
| TestRpcWorkflowTemporal | pass |  | |
| TestRpcWorkflowTemporalContinueAsNew | pass |  | |
| TestRpcWorkflowTemporalContinueAsNewWithMemo | pass |  | |
| TestRpcWorkflowTemporalContinueAsNewWithMemoAndEncryption | pass |  | |
| TestRpcWorkflowTemporalWithMemo | pass |  | |
| TestRpcWorkflowTemporalWithMemoAndEncryption | pass |  | |
| TestS3GetSetDataAttributesTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestS3GetSetDataAttributesWithInitialDataTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestS3WorkflowInitDataAttributesTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestS3WorkflowStartInputTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestS3WorkflowStateInputOptimizationTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestS3WorkflowUpsertDataObjectsTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestSearchRunsTemporal | pass |  | |
| TestServerInfoTemporal | pass |  | |
| TestSetDataAttributesTemporal | pass |  | |
| TestSetSearchAttributes | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestSignalWorkflowTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestSkipStartFlowTemporal | pass |  | |
| TestSkipStartFlowTemporalContinueAsNew | pass |  | |
| TestStartDelayTemporal | pass | behavior-difference | Passes; asserts that server-zd rejects flow_start_delay_seconds > 0 with InvalidArgument (C8). |
| TestStartFlowNoOptionsTemporal | pass |  | |
| TestStateApiFailAndProceedTemporal | pass |  | |
| TestStateApiFailTemporal | pass |  | |
| TestStateApiRetryAfterTemporal | pass |  | |
| TestStateApiTimeoutTemporal | pass |  | |
| TestStateExecuteMethodFailAndProceedTemporal | pass |  | |
| TestStateExecuteTimeoutRecoveryTemporal | pass |  | |
| TestStepCancellationTemporal | pass |  | |
| TestStepLineageTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestStepStateLoadingTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestStreamAPITemporal | pass |  | |
| TestStreamDisabledTemporal | pass |  | |
| TestStreamStepWorkerStreamingTemporal | pass |  | |
| TestSubFlowConditionTemporal | pass | behavior-difference | Harness gap fixed. Passes; server-zd SubFlow request IDs add the run attempt and condition index. |
| TestSynchronousUpdateRequestIDTemporal | pass | behavior-difference | Ported from go-server-only with transactional RPCs. zd-bug 5 (#1005) fixed by #1038, so binary mode expects one Worker invocation. |
| TestTerminalProducerDrainTemporal | pass |  | Ported from go-server-only in this change; runs against dex-server-zd. |
| TestTimerFlowTemporal | pass | behavior-difference | Harness gap fixed. Passes; asserts the reset difference. zd-bug 6 (#1007) fixed by #1037, so the skipped timer status runs the Go Server assertion. |
| TestWaitForAttributeBlobBackedTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestWaitForAttributeTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestWaitForStateCompletionTemporal | pass |  | Harness gap fixed in this change; runs against dex-server-zd. |
| TestWebAPITemporal | skip | harness-gap | Existing #1003 guard retained for applicable history/state scenarios; Go Server snapshot and local-activity scenarios were not copied. |
| TestWfStateOptionsDataAttributesLoading | pass |  | |
| TestWfStateOptionsSearchAttributesLoading | pass |  | |
| TestWorkflowCanceledTemporal | pass |  | |
