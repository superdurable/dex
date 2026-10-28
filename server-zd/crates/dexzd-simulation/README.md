# dexzd-simulation

## Purpose

Deterministic simulation tests that run the server-zd engine against simulated object storage, clocks, Workers, and crashes.

## Apply simulation

The first stage tests `dexzd-apply` without the engine. A seeded model of clients, the owner,
and pull Workers proposes commands. `ApplyChecker` applies each command twice on the committed
state and once on a re-decoded twin, requires identical outcomes, and then checks the state
invariants: lock holders and FIFO waiter order (L1, L3, L4), lost updates on guarded keys (L2),
fresh snapshots (S5), one result per attempt (S2), value holder sets and garbage rows (I1),
history event order, the timer index, and agreement of the Flow directory and run records.
Attribute Sync outbox checks cover unacknowledged changes keeping their entries (AS1), acks
deleting only the entries they cover (AS2), and the pending index matching the entries (AS3).
A second stream with its own seeded draws drives one Flow, `account-sync`, that syncs to two
Attribute Stores and receives acks. It never changes the main stream, whose per-seed digests
`regression_seeds_keep_their_main_command_trajectories` pins. A third stream, also with its own
draws, drives `order-recovery-1` and `order-recovery-2`. Their Steps and timeout handlers carry
WaitFor and Execute failure policies, recovery targets, and retry total durations of 1 to 5
seconds. Workers there also report `RETRY_DEADLINE` failures. SearchOrder rows must match the
search documents (SO1).

The Attribute Sync stream also attaches permission mappings to its plain
Attribute batches. They compare post-batch values and accumulate one inline
permission, preserving the main stream's pinned trajectories. Coverage counts
require an applied mapping batch; they do not count projected permission writes or
verify permission preservation. The apply stream has no stored mapping inputs or
permission rebuild oracle, and the engine workload generates no permission mappings.
[Issue #1016](https://github.com/superdurable/dex/issues/1016) tracks those gaps:
varied grants across batches, mappings to stored Attributes outside the batch,
recorded-versus-rebuilt semantic-point hashes, and expected rejections for collected
inputs. The follow-up also includes permission mappings in the engine workload and a
seeded mutant that drops a rebuilt grant and must fail the permission oracle.
Collected mapping inputs and large permission sets are covered by deterministic
`dexzd-apply` regressions.

Worker invocation commands carry the run attempt that dispatched them. Redelivering a result
preserves that stamp across resets, so apply rejects results from abandoned run attempts just
as it does for real Workers. Removing or replacing the stamp can make an old result write an
Attribute while another Step holds its lock, falsely reporting L2. The regression seeds
`10906685018` and `2204788261` cover this model correction from issue #1041.

Correcting Worker stamps intentionally refreshed the existing trajectory digests. Mutation
checks for this correction caught premature value reclamation and stale retry snapshots with
`1592590336`, Attribute Sync during history rebuild with `161503589` and `879068896`, and
request deduplication during replay with `879068896`. The old cases `476371529` and `4200525811`
remain deterministic trajectory checks; the other pinned cases cover their former failures.

```bash
cargo test -p dexzd-simulation                                   # fixed seed set
DEXZD_SIMULATION_SEEDS=500 DEXZD_SIMULATION_COMMANDS=1000 cargo test -p dexzd-simulation
DEXZD_SIMULATION_SEED=1592590336 cargo test -p dexzd-simulation  # reproduce one seed
```

A failing seed prints its seed, the failing command index, and its command sequence immediately.
After shrinking, it prints the shortest sequence found that `replay_commands` reproduces.

The fixed test runs 64 seeds of 400 commands, plus every case in `regression-seeds.txt`.
Each line in that file contains a decimal seed and the command count needed to reach its failure.
The checked-in seed also runs for 1,000 commands. Nightly runs set `DEXZD_SIMULATION_BASE_SEED`
to a printed random starting seed, then use `DEXZD_SIMULATION_SEEDS` consecutive seeds and
`DEXZD_SIMULATION_COMMANDS` commands per seed. The single-seed override runs only that seed.
Manual runs accept 1–1,000 random seeds and 1–2,000 commands per seed, with at most 500,000
random commands total. The fixed regression seeds run in addition to that random budget.

When a nightly run fails, download its failure artifact. The first failing seed gets a report
immediately, before shrinking starts. Shrinking is limited to 200 replays or 30 seconds, and the
completed report replaces the initial sequence with the shortest one found. The nightly test
stops after that failure, has a 50-minute command limit, and uploads the log and reports even if
the command fails. Reproduce the failure from `server-zd/` with
`DEXZD_SIMULATION_SEED=<seed> DEXZD_SIMULATION_COMMANDS=<failing index + 1> cargo test -p dexzd-simulation`.
Add `<seed> <failing index + 1>` as a line in `regression-seeds.txt`, then run the fixed test.
The command count keeps failures beyond the default 400 commands in every PR run.

## Engine simulation

The second stage runs the real engine: `EngineNode`, the Flow coordinator registry, and the effect
runner, assembled as the server assembles them. Everything runs on one current-thread Tokio
runtime with paused time, and every choice derives from one seed.

- **Object storage.** One shared `InMemoryObjectStore` has S3 conditional-write semantics. Each
  node process sees it through its own seeded fault layer. The layer injects slow responses,
  HTTP 5xx and 503, 409 conflicts, timeouts, and lost responses. A failed or timed-out write may
  still take effect. A lost response hides only a write that took effect; a write that failed,
  such as a 412, returns its own result. Old-epoch processes produce real 412 races.
- **Processes.** A crash first cuts the process off from storage. Every write runs on a task
  that the shared storage owns, so a write already sent lands even after its process and
  caller are gone: at its normal time, during the successor's recovery, or just after the
  successor starts serving. Some crashes wait until two log writes are in flight. The
  successor's local store is kept, lost (restored from the newest snapshot), or rolled back to
  an earlier copy. Some successors take over while the old-epoch process still runs and writes,
  because their wall clock is ahead. Wall clocks also jump forward and back.
- **Workers and clients.** Simulated pull Workers sit behind the engine's dispatch ports. They
  succeed, fail, hang, run slowly, send heartbeats with and without values, return the same
  attempt's result again, disconnect, or are missing. A WaitFor of a client's Flow may wait for
  a child Flow, which the outbox starts; child Flows start no children of their own. Clients
  start, stop, write, publish, delete, invoke RPCs, and read, retrying unavailable calls with
  the same request ID. They also write and delete a hard-delete Attribute, whose values live in
  object storage.
- **Child-wait probe.** In the fixed set, one probe client per client Flow ID keeps a locking
  `awaitChildResult` RPC in flight on each run whose WaitFor result waits for a child Flow.
  The Worker holds that RPC until the parent's Execute is dispatched or the RPC deadline
  passes. The child's result therefore arrives while a transactional RPC of its parent is
  admitted, and the parent's close hold dry-runs it. Ordinary client RPCs alone produced that
  in only a few of the 64 seeds, so the coverage check failed when unrelated changes moved the
  trajectories. The probe draws only from its own seed, which is the simulation seed mixed
  with a constant. The redeploy and Step failure profiles run no probe.
- **Determinism.** The engine runs frame applies, state reads, and snapshot uploads on the
  calling task (`BlockingWorkPlacement::CallingTaskForTest`), and every engine `select!` is
  biased. Every ID the engine persists comes from one `SeededEngineIdSourceForTest` that all
  processes of a seed share, so the trace keeps engine IDs as minted.
- **Replay checks.** Every node runs with `debug_replay_checks`. Appliers apply each command
  twice and compare the outcomes, and speculative outcomes are compared with committed ones. Any
  difference halts the partition, which fails the seed.

### Invariants

A shadow `ApplyChecker` collects the canonical committed log from every process's applied
frames. It runs the stage-1 checks on every new command, so the lock, snapshot, value, and
directory invariants hold on the committed history. The engine simulation adds these checks:

1. **Acknowledged writes survive.** Every write a client saw succeed is in the canonical log,
   and every recovered store equals the never-crashed twin (invariant 5).
2. **No speculative or stale reads.** Each GetAttributes and GetChannelMessages response, as a
   whole, equals the committed state of the Flow's newest run at a frame its node applied during
   the call. The shadow keeps each client Flow's state history by frame, and the call's window
   runs from the node's applied position when the call began to the one when it returned. A
   value that a later applied write overwrote fails, as does an uncommitted one.
3. **One result per attempt.** The shadow rejects a second applied result (S2). Any frame
   applied again after recovery must reproduce its first outcome exactly.
4. **Locks.** The shadow checks L1 to L4 after every command, and the recovered store passes
   `check_flow` again, including right after a takeover while the old-epoch process still runs.
   A successor replays every batch below its EpochStart before it serves, so its store never
   lags the shadow. The old-epoch process's own store lags once the successor applies frames.
   It passes `check_flow` at each periodic check during the overlap and once more before the
   process is discarded. Each check uses the window points the shadow retained at the store's
   applied position. The summary field `recovered_checks_with_older_window_points` counts checks
   that needed them; `recovered_checks_during_old_epoch_overlap` and `old_epoch_store_checks`
   count the takeover and old-epoch checks.
5. **Recovered runtime equals a never-crashed one.** The twin is the store of a node that
   applied the canonical log and never crashed: the shadow's rows, partition metadata derived
   from the canonical commands, and node metadata. `compare_store_views` compares every column
   family of the node's store with it. Only the four fields that recovery legitimately changes
   are canonicalized: the store lineage, the snapshot number, and each partition's applied
   position and epoch. The twin cannot model the node-local hard-delete upload ledger, so a
   comparison is skipped and counted while the ledger holds uploads. The summary field
   `state_comparisons_skipped_for_upload_ledger` holds the count, and the test prints it on its
   `twin comparisons:` line next to the comparisons that ran. The recovered store equals
   the twin, so timers, method calls, outbox rows, and hard-delete rows match. Every crash
   counts the timers, running attempts, undelivered outbox rows, and pending hard-delete objects
   it interrupts.
   Recovered processes must deliver each interrupted outbox row and delete each interrupted
   hard-delete object from storage. After a crash at a quiet point, the successor dispatches
   every attempt that was running again. After a calm tail, every due timer has fired, no
   method call is unfinished, and no outbox row or hard-delete object is left waiting.

The log itself is checked too. Copies of one log position must hold the same commands, and a
partition never applies a new frame below one it already applied. No process applies a frame
whose batch an EpochStart of a newer epoch at a lower seq of the same stream voided
(`voided-batch-applied`).

The fixed seed set also asserts that these checks had something to check. It requires every
fault kind with each effect it can have on a call, such as a timed-out write that did or did
not take effect. It requires every restart mode and Worker behavior, and crashes during
recovery. It also requires 412 races on log and lease objects. An old-epoch window opens only
when a successor's lease write takes effect; the seed set requires log and lease writes that
old-epoch processes started, and frames they applied, inside such a window. Log writes that
landed after their process died are required, along with EpochStarts written below an
already landed batch (the recovery void and fill path) and successors that stopped on an
older-epoch batch at their next seq. Log cleanup deletions, snapshot restores during starts,
and rejected duplicate results are required too. So are crashes that interrupted each kind of
outstanding work, recovered deliveries and deletions of that work, running attempts compared
after the quiet crash, store comparisons right after a recovery, and live runs in the final
state. Recovered-store checks during an old-epoch overlap and checks of old-epoch stores are
required too. Some crashes wait until two log writes are in flight or cross-Flow work is outstanding.
The set must also dry-run a child result for an admitted transactional RPC of its parent.

### Not covered yet

These section 13.1 items need later stages:

- Byte-identical snapshots across dispatches of one attempt, and Step input, locals, and
  condition results unchanged across attempts (section 8.1). The shadow checks only fresh
  snapshots (S5).
- Eventual progress of lock waiters beyond the calm-tail check (section 8.3).
- Time travel rebuilds and their hashes (section 8.4); clients do not call ResetFlow, so the
  replay checks verify no time-travel target.
- Value lifecycle beyond I1: I3, liveness, reads within the window, and the G byte limit
  (section 10).
- Store comparisons while the hard-delete upload ledger holds uploads; about 3% of them.
- Hard-delete Attributes that Workers write, and the hard-delete upload ledger after a crash
  between upload and commit (section 10.3).
- Worker Stream writes, and the Worker matching layer, which the simulated Workers bypass.
- More than one node ID, and foreign writers of the manifest and lease.

### Redeploy profile

The fixed set keeps the engine's default history window of 10,000 semantic events and one node
incarnation, so its windows never advance and its log streams never close. A second test,
`redeploys_with_a_short_history_window_keep_every_invariant`, runs the
`RedeploysWithShortHistoryWindow` profile on its own seed group: 16 seeds from `1323171840`
(`0x4ede0000`). The profile changes every trajectory, so the fixed set's seeds and defaults stay
as they are.

- Runs use a four-event semantic history window with a prune chunk of one event, so windows
  advance and prune within a run.
- Half of the restarts and takeovers start a new node incarnation, as a redeploy does. That
  start seals the previous log stream and closes it in the manifest.
- Nodes take no snapshots. A start with a lost store therefore replays every closed stream from
  its first batch, and a kept or rolled-back store replays closed streams after its applied
  position.
- One injected fault fails a start, and a whole-log replay grows with the run. Once more than
  100 log batches have landed, a start's fault probability shrinks in proportion to the batch
  count. A start then expects about as many faults as a start that replays 100 batches.
- Before about half the active-phase crashes, and at every takeover, the process's next log
  write stalls until the process dies and never lands. Before such a crash, the profile waits
  until a later log write lands. That batch then sits above a hole, which the successor's
  EpochStart fills, so the batch is void.

The test requires incarnation changes, starts whose closed-stream replay covered a batch that an
EpochStart voided, pruned semantic events, recovered-store checks during an old-epoch overlap,
old-epoch store checks, and checks that used older window points. The summary fields are
`incarnation_changes`, `closed_stream_replays_over_voided_batches`,
`final_pruned_semantic_events`, `recovered_checks_during_old_epoch_overlap`,
`old_epoch_store_checks`, and `recovered_checks_with_older_window_points`. It reads the same
environment variables as the fixed set, without the regression seeds.

### Step failure profile

The fixed set and the redeploy profile draw no Step failure policies. A third test,
`step_failure_policies_keep_every_invariant`, runs the `StepFailurePolicies` profile on its own
seed group: 32 seeds from `1604386816` (`0x5fa10000`). It uses the fixed set's environment.

- Some Steps let a failed WaitFor proceed to Execute. Others send a failed Execute to a
  `RecoverStep` recovery Step.
- Some retry policies have a total duration of 1 to 5 seconds. The engine ends such a method
  with a `RETRY_DEADLINE` failure once the duration expires.
- Flow timeouts last 5 to 30 seconds, so they expire before most runs close. Every timeout runs
  a timeout handler, which makes at most two attempts. In 60% of the Flows, a failed handler
  proceeds to `RecoverStep`.

The test requires each outcome that `step_failure_recoveries` counts: `wait-for-proceed`,
`execute-recovery`, `timeout-handler-recovery`, and `retry-deadline`. It reads the same
environment variables as the fixed set, without the regression seeds.

### Pinned trajectories

`pinned_seeds_keep_their_canonical_command_trajectories` runs each line of
`engine-regression-seeds.txt`, the first three fixed-set seeds, and the first redeploy-profile
seed. Each seed's `canonical_command_digest` must equal its pinned value. The digest chains
xxh3-128 over every committed command, in the order the shadow accepted them. The pinned values
record the expected trajectories for the current startup fault model. Four pins that are not active
regression seeds, `3856990760`, `FIRST_SEED`, `FIRST_SEED + 1`, and `FIRST_SEED + 2`, were
refreshed when an exhausted Step failure started closing its Flow with the Go Server's failure text.
A child Flow's failure message reaches its parent as the `error_message` of a committed
`InboxMessage` command. Only those bytes changed, so the trajectories stayed the same. The two
active regression seeds kept their digests. A failing run prints each moved pin's observed digest
in hex.

Any change that moves a regression seed's trajectory can make the seed stop reproducing its
failure. An engine change can do that as well as a workload change. Without the pins, no test
would fail. Draw new choices only in a profile with its own seed group, as the redeploy and Step
failure profiles do. When a pinned digest changes, revert each regression's fix and check that
its seed still fails, or find a new seed. Then update the pinned value.

`3856990458` and `3856990400` reproduce the failure of reads of a purging run, which omitted a
hard-delete Attribute right after the purge deleted its object. Each fails within 120 seconds
when the fix is reverted. `3856990760` found that failure first, but a later change to object
storage reads moved its trajectory, so it no longer reproduces it. `3856990875` reproduced it
until the child-wait probe moved its trajectory.
Combining the child-wait probe with later engine changes moved every pin again. Both active
regression seeds still fail at the same simulated times when the fix is reverted.

### Running

```bash
cargo test -p dexzd-simulation --test engine_simulation         # 64 seeds of 120 s, 1-2 minutes
DEXZD_SIMULATION_SEEDS=1000 DEXZD_SIMULATION_SECONDS=600 \
  cargo test -p dexzd-simulation --test engine_simulation \
  the_engine_keeps_every_invariant_through_faults_and_crashes -- --exact     # longer budget
DEXZD_SIMULATION_SEED=7 cargo test -p dexzd-simulation --test engine_simulation \
  the_engine_keeps_every_invariant_through_faults_and_crashes -- --exact --nocapture
DEXZD_SIMULATION_SEED=3856990214 DEXZD_SIMULATION_FAULT_PERCENT=5 \
  cargo test -p dexzd-simulation --test engine_simulation \
  the_engine_keeps_every_invariant_through_faults_and_crashes -- --exact --nocapture
DEXZD_SIMULATION_SEED=1323171844 cargo test -p dexzd-simulation --test engine_simulation \
  redeploys_with_a_short_history_window_keep_every_invariant -- --exact --nocapture
DEXZD_SIMULATION_SEED=1604386820 cargo test -p dexzd-simulation --test engine_simulation \
  step_failure_policies_keep_every_invariant -- --exact --nocapture
```

`DEXZD_SIMULATION_SEEDS` and `DEXZD_SIMULATION_SEED` also select the apply simulation's seeds
when both tests run. A failing seed prints its reproduction command and the last 400 trace events.
`DEXZD_SIMULATION_FAULT_PERCENT` overrides the engine simulation's sampled object-storage fault
rate for a targeted run; omit it for the fixed and nightly seed sets.
`DEXZD_SIMULATION_PRINT_EVENTS=1` prints the trace tail of a passing seed. A self-test runs seeds,
including one seed of each other profile, twice and requires identical raw traces, final local
stores, and stored objects, leases included. It compares the two final local stores with `compare_store_views` and no canonicalized
fields.

`DEXZD_SIMULATION_BASE_SEED` replaces the fixed set's first seed, so a run covers
`DEXZD_SIMULATION_SEEDS` consecutive seeds from there. Every run except a single-seed run also
covers each line of `engine-regression-seeds.txt`: a decimal seed and the active seconds it
failed with. A seed in both runs for the longer active phase, whose trajectory starts the same. `DEXZD_SIMULATION_FAILURE_DIR` names a
directory that receives a `<seed>.txt` report for a failing seed before the test panics. Each
report holds the violation, the reproduction command, and the last 400 trace events.

### Nightly

The nightly workflow picks separate random base seeds for head and the latest release, then
partitions each target's consecutive seed ranges without overlaps or omissions. Head uses a
32-bit base; the release adds 2³³ to its own random 32-bit base, keeping the targets' random
ranges disjoint. Checked-in regression seeds still run on both targets.
Its apply jobs run 500 random seeds of 1,000 commands across
five shards of 100 seeds. Its standard engine jobs run 250 random seeds of 300 simulated
seconds across three shards of 83, 83, and 84 seeds. Each shard also runs the checked-in
regression cases and retains the suite's coverage assertions. Manual inputs still specify the
total seed budget: apply shards contain at most 100 seeds, and engine shards remain at least
64 seeds. Manual engine runs accept 64–1,000 seeds and 60–1,200 seconds per seed, with at
most 75,000 simulated seconds in total.

Nightly test builds use `CARGO_PROFILE_DEV_OPT_LEVEL=1`. This optimizes the simulation and its
dependencies while retaining the test profile's debug assertions and overflow checks.
Compilation has a separate 25-minute command limit, followed by the
apply suite's 50-minute limit or each engine suite's 55-minute limit. The job limit is 90 minutes,
leaving time for setup and artifact uploads. Sharding preserves the total random workload;
regression cases run in every shard, so regression coverage increases.
These jobs build without the regular CI cache, whose unoptimized artifacts use a different
Cargo profile and cannot satisfy the optimized build.

Every successful seed prints `simulation completed seed: <seed>`. The workflow uses the
runner from its own commit for both refs. For releases predating this output, the runner adds
only this print to the successful test-loop branch before compiling; unknown test layouts fail
explicitly. No production source, seed trajectory, or coverage assertion is changed.

The command runner streams output to the job log and an artifact file. It records wall time
and distinguishes `timeout`, `invariant failure`, `command failure`, `acceptance failure`, and
`interrupted` from `success`. A simulation that exits successfully without completing a seed
fails with `no completed seeds`; builds and seeded-bug acceptance do not require seed progress.
Only the acceptance runner's anchored `Timed out after` marker identifies a nested timeout;
the older baseline's ambiguous failure text remains an acceptance failure.
On timeout the runner kills the command's process group and prints the base seed and last
completed seed, or `none` if no seed finished. A recorded invariant failure always takes
precedence, including `invariant failure (timed out while shrinking)` when shrinking times out.
The original invariant report remains in the failure artifact. The summary
includes the shard base seed, seed count, elapsed seconds, last completed seed, failing seeds'
reproduction commands, and the standard engine's `twin comparisons:` line. Build and test logs
are uploaded even when a command fails. Build results have separate output keys and summary
lines, so an interrupted simulation cannot report its build's success. Run
`make server-zd-simulation-runner-test` from the repository root to check process supervision,
release instrumentation, and seed partitioning. A separate PR workflow runs these tests when
the runner or its workflow changes; nightly simulation jobs do not depend on that test job.

A separate nightly job runs the redeploy profile with its own random base seed: 48 seeds of 300
simulated seconds. Its seeds can cost more than the fixed set's
because lost-store starts replay the whole log. Manual runs accept 16–1,000 seeds and 60–600
seconds per seed, with at most 20,000 simulated seconds in total. The job has the same
55-minute command limit, failure reports, and summary as the fixed set's job.

Another nightly job runs the Step failure profile the same way, with its own random base seed,
48 seeds of 300 simulated seconds, and the redeploy job's limits.

Every nightly simulation job, including the apply simulation and seeded-bug acceptance, runs on
two refs. One is the workflow's commit, which is main for scheduled runs. The other is the
latest stable server-zd release: the newest `server-zd/vX.Y.Z` tag in version order.
Pre-release tags such as `server-zd/v1.0.0-rc1` never count, as with the release image's
`latest` tag. A resolver job finds that tag at run time. Until a stable release tag exists, it
logs that and runs only the workflow's commit. Failures on the released line therefore show up apart from changes on main.
A release that predates the redeploy profile skips that job with a log line. Each job's summary
and failure artifact name the ref they ran on.
