# Time Travel API (ForkRun)

In-place time travel restores a run's **live** execution state to a past
history point on the **same** `run_id`. Prior history is kept; a `RunFork`
marker records the rewind; new events append after the marker.

## API

`RunsService.ForkRun`:

```protobuf
rpc ForkRun(ForkRunRequest) returns (ForkRunResponse);

message ForkRunRequest {
  string namespace = 1;
  string run_id = 2;
  int64 to_event_id = 3;
  string reason = 4;
}
```

Fork targets:

| Event | Snapshot required |
|-------|-------------------|
| `RunStart` | No — re-derive starting steps like `StartRun` |
| `StepExecuteCompleted` | Yes — non-terminal (`StopDecision` not COMPLETE/FAIL) |
| `StepWaitForCompleted` | Yes |
| `StepsUnblocked` | Yes |

Rejected: `RunStop`, `ChannelPublish`, `RunFork`, terminal `ExecuteCompleted`,
events without a snapshot (pre-feature history).

## RunStateSnapshot

Embedded on the three step payloads above at history write time (post-mutation
merged view). Fields:

- `state_map` — full replace on fork
- `unconsumed_channel_messages` — full replace (`map<string, ChannelMessages>`)
- `step_exe_id_counters` — full replace
- `active_step_executions` — full replace; `RetryState` omitted (retries restart)
- `external_channel_message_counter`

**Not** in snapshot (live row / derived on reapply):

- `worker_request_counter` — bumped by `+1000` on fork (evict stale worker)
- `step_method_exe_counter` — kept at live value (never regress)
- Durable timer fields — derived from restored `ActiveStepExecutions`
- Ownership: `worker_id` cleared; heartbeat timer cleared

## Reapply flow

1. Load target history event; validate forkable + snapshot (if required).
2. Replace maps/counters/channels from snapshot (or synthesize for `RunStart`).
3. Strip retry state from active steps (via snapshot omission).
4. Clear worker ownership; bump `worker_request_counter`.
5. Derive timer/status: `Pending` + resume dispatch if invoking steps exist;
   else `AllStepsWaiting` + arm timer, or `DurableTimerFired` if `minFireAt <= now`.
6. Append `HistoryRunFork` via OpsFIFO; update visibility.
7. Best-effort `StopRequested` to previous worker (DEBUG on notify failure).

## Limitations

| Case | Behavior |
|------|----------|
| Server-side promote only (no history) | Cannot fork to that point |
| `ChannelPublish` alone | Not a fork target; channel state lives in later snapshots |
| Pre-snapshot events | `InvalidInput` |
| History after fork | Remains in timeline; graph UI uses post-fork branch only |
| Blob prune | Future prune/delete must retain blobs referenced by kept snapshots |

## Metrics & logging

- `fork_run_requests` (Info) — tag `outcome=success|invalid|conflict|internal`
- `fork_run_latency` (Info)
- INFO on successful fork; DEBUG on rejections and StopRequested notify failure

## Web UI

- Timeline shows all events; `RunFork` renders as an orange divider.
- Eligible events expose **Fork to here** (timeline view).
- Step graph uses events after the latest `RunFork` (plus the fork target event).

See also [history-store-design.md](history-store-design.md).
