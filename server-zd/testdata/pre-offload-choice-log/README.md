# testdata/pre-offload-choice-log

Object storage contents and committed state that server-zd recorded with its format-compat
generator at commit `e2e451513`, the last commit before runs recorded their own history offload
choice in `RunSettings.offload_history`. In that binary a partition offloaded run history only
after a `SetHistoryOffloadMode` command enabled it. Partitions 2 and 3 never had the mode before
the snapshot, which is the state of every partition of a `serve` data directory before 0.1.23.

The workload is that commit's `workload.rs` with these changes:

- `run_before_snapshot` ends with `legacy_runs_record_history_without_offload`, and
  `run_after_snapshot` no longer calls it. The snapshot therefore holds these runs:
  - `legacy-running`, in partition 2: a running run with an eight-event window and small
    segments that recorded 30 rounds of history without segment state. Its window pruned the
    oldest records locally.
  - `legacy-closed`, in partition 2: a run with a 60-second retention that recorded four rounds
    without segment state and was canceled.
  - `legacy-migrating`, in partition 3: a run that recorded 30 rounds without segment state,
    then sealed two migration segments after the partition turned offload on.
- `run_after_snapshot` ends with two more scenarios, which only the log holds:
  - `reused`, in partition 2: a run that recorded four rounds, was canceled, and was purged
    when its 60-second retention ended. A run that disallows ID reuse then started with the
    same Flow ID and recorded six rounds. It is still running.
  - `small-budget`, in partitions 2 and 1: each run rewrote one 200-byte value 400 times under
    a 32 KiB state budget, in batches of 20. In partition 2 the window deleted superseded
    values, so every rewrite applied. In partition 1, which offloads, the rows of superseded
    values counted as state until an upload that never came, so the later rewrites were
    rejected with RESOURCE_EXHAUSTED.

None of these runs records an offload choice. A node of a later binary must replay every
command with the semantics of the binary that recorded it.

The `pre_offload_choice_log` tests of the `format_compat` target, in
`crates/dexzd-engine/tests/format_compat/pre_offload_choice_log.rs`, replay it from the snapshot
and from the start of the log. One checks that the debug replay checks stop no partition, also
at the `travel-flow` TimeTravel whose staged result still holds the rows of completed Steps.
The strict one compares the replayed state with `state.txt` row for row, with the long values
that `pre_offload_choice_rows.txt` pins from the recording binary. It allows only what later
apply derives from the recorded rows: the rows of ended Steps that apply deletes since #1429,
the checkpoints, history row metadata, and run record counters without them, the prune
cursors that apply maintains since #1427, and the ended Step row sweep of #1430. The replay
must delete exactly the Step rows that later apply drops. A row that the restored snapshot held
unchanged follows the sweep, which deletes the deletable rows of each run that a replayed
command reaches and marks that run's sweep complete. Every other row follows the command that
ended its Step. A checkpoint that a replayed command wrote lacks the same rows.

The `workload::legacy_history` tests, in
`crates/dexzd-engine/tests/format_compat/workload/legacy_history.rs`, restore it and check that
the purge of the reused Flow ID stays applied and its replacement run survives; that runs of
partition 2 keep their history local and never migrate; and that the small-budget runs keep
the admission results of their release while a run started after recovery never stalls.

It is a test fixture, not a `v<N>` version: the generator never writes it and the version pins do
not cover it. Since 0.1.23 every run records its offload choice, so no later generator records
runs like these unless it builds the older commands on purpose. Never edit or regenerate it.
