# testdata/pre-attempt-handout-log

Object storage contents and committed state that the last server-zd before Step attempt
hand-out (commit `9f4344441`) recorded with its format-compat generator. Its heartbeats and
Worker results carry no task ID, its attempt failures carry no guard field, its time travel
carries no `should_restore_running_as_pending`, and its Step executions carry no attempt
progress.

The workload is that commit's `workload.rs` plus one scenario at the end of the log, after the
snapshot: a Flow whose locking `ShipStep` attempt is Running when an Attribute write happens. The
attempt completes, a HistoryEventTime time travel goes back to that write and restores the
attempt as Running, and the restored attempt completes again without a task ID.

`run_records.txt` holds the full value of every committed run record, one `<key hex> <value
hex>` line each, because `state.txt` only hashes long values. The same commit's generator wrote
it from the same committed view, with `objects/` and `state.txt` byte for byte unchanged.

The `old_log_replay` test in `crates/dexzd-engine/tests/format_compat/` replays it, with and
without its snapshot, and requires the committed state of that binary. Step execution rows and
attempt checkpoints must match after clearing only the attempt progress fields that the newer
binary adds. Run records must match without their byte counters: storage usage,
`row_write_bytes_since_checkpoint`, and `last_checkpoint_state_bytes`, which count those bytes. It
is a test fixture, not a `v<N>` version: the generator never writes it and the version pins do
not cover it. Never edit or regenerate it.
