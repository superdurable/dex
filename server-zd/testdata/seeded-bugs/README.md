# Seeded bug acceptance

Each patch introduces one deliberate defect in a scratch Git worktree. The
baseline and mutants use the same committed HEAD and separate scratch Cargo targets.
The caller's source and build tree stay unchanged. Run from the repository root:

```bash
script/server-zd-seeded-bugs.sh
```

The runner first requires the unpatched apply simulation, engine simulation, and
engine redeploy profile to pass. It then applies each patch alone, runs the suite
the table names, and requires its named invariant to fail. A compile failure,
timeout, or passing mutant fails acceptance. It reports every gap before exiting
nonzero. It prints each Cargo target and requires the baseline to compile the
simulation crate from its own fresh worktree.

Each mutant's Cargo target starts as a copy of the baseline's, so third-party
crates are not built again. Every workspace crate compiles again from the
mutant's worktree, and the runner requires the patched crate to do so. Each build
has a 25-minute limit. Each suite has a fixed budget and its own limit, apart
from the build:

- `apply`: 64 seeds starting at `1592590336` (`0x5eed0000`), with 400 commands
  per seed, and a 300-second limit that includes shrinking.
- `engine`: the fixed engine set of 64 seeds starting at `3856990208`
  (`0xe5e50000`) plus the engine regression seeds, with 120 simulated seconds
  per seed, and a 900-second limit.
- `redeploy`: the engine redeploy profile's 16 seeds starting at `1323171840`
  (`0x4ede0000`), with 120 simulated seconds per seed, and a 900-second limit.

A run stops at the first failing seed. Time to detection below is the
simulation's wall time on a laptop, after the build.

| Patch | Deliberate defect | Suite | Expected detector | Status |
| --- | --- | --- | --- | --- |
| `wall-clock.patch` | Apply uses the process wall clock instead of the command timestamp. | `apply` | `A2-apply-twice` compares byte-identical writes. | Caught at seed `1592590336`, command 0, in 1.2 s. |
| `hash-map-iteration.patch` | Attempt snapshots emit Attributes in `HashMap` iteration order. | `apply` | `A2-apply-twice` or `A2-apply-redecoded` compares byte-identical writes. | Caught at seed `1592590336`, command 30, in 1.3 s. |
| `immediate-reclaim.patch` | After the last slot holder leaves, R6 records that death point while an attempt snapshot still pins the value. It deletes the value when the final pin leaves after that death point reaches window floor W; otherwise it queues G at the old death point. | `apply` | `I1-window-value-reference` must catch a retained point whose in-flight snapshot needs the deleted value. | Caught at seed `1592590336`, command 107, in 3.8 s (the retained-window invariant merged in #839). The targeted `snapshot_pin_survives_window_advance_and_collects_after_attempt_completion` integration test also fails on the mutant and passes without it. |
| `retry-fresh-snapshot.patch` | A retry reloads current state and replaces its retained first snapshot, transferring value pins. | `apply` | `S5-retained-snapshot` must catch changed input after an intervening write. | Caught at seed `476371563`, command 38, in 0.6 s. |
| `ack-before-persist.patch` | A client write returns its speculative reply immediately after proposal, before the object-store log write is durable. | `engine` | `acknowledged-write-lost`: an acknowledged command must survive a crash and restart in the canonical committed log. | Caught at seed `3856990208`, simulated time 16.158 s, in 2.2 s: an acknowledged InvokeRpc had no committed command. |
| `skip-epoch-check.patch` | Closed-stream recovery replays batches from an older epoch after a new owner starts. | `redeploy` | `voided-batch-applied`: recovered state must exclude commands voided by the epoch transition. | Caught at seed `1323171841`, simulated time 46.365 s, in 21.8 s: a start replaying closed stream 1 applied the epoch-1 frame at seq 120. |
| `speculative-read-leak.patch` | GetAttributes reads the speculative overlay. | `engine` | `speculative-read`: a client read must see only committed state, including before and after a failed commit. | Caught at seed `3856990212`, simulated time 103.310 s, in 14.4 s: GetAttributes returned state of no committed generation in its read window. |

The immediate-reclaim patch is the original R6 failure mode. The apply model
needs an attempt snapshot pinned across a slot overwrite and window advance,
followed by attempt completion and time travel to an in-flight point.

The skip-epoch-check patch changes only the replay of closed log streams. The
fixed engine set runs one node incarnation, so its streams never close and it
cannot catch this mutant. The redeploy profile starts new incarnations, which
close streams, and stalls log writes before crashes and takeovers. A later batch
then lands above a hole that the successor's EpochStart fills and voids. A later
start replays the closed stream below that batch.

The nightly workflow runs `script/server-zd-seeded-bugs.sh` in its own job, on
main and on the latest stable `server-zd/vX.Y.Z` release tag. A mutant that the
simulation does not catch fails that job.
