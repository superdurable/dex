# testdata/pre-rpc-publication-rejection-log

Object storage contents and committed state that server-zd recorded with its format-compat
generator at commit `1d37cc7f5`, the last commit before apply rejected an RPC that publishes to a
Channel while a Flow close is pending. In that binary such an RPC committed its publication, its
Attribute writes, and its reply. A TimeTravel that met such an RPC while reapplying input
reapplied all of it. Neither command records the rule, so a later binary must replay them with
these semantics.

The workload is that commit's `workload.rs` with these changes:

- `run_before_snapshot` runs only `close_held_by_an_admitted_rpc` for two Flows of type
  `HeldCloseFlow`. In each, `OnlyStep` force-completes while an admitted transactional RPC is in
  flight, so the close is pending. The snapshot holds both pending closes.
- `run_after_snapshot` runs only these scenarios, which only the log holds:
  - `held-close-publish`: `rpc_publication_commits_and_records_the_close`. The admitted RPC
    `rpc-publish-closing` commits a publication to `orders`, an Attribute write, and a reply
    that request deduplication keeps. The same command then records the close, so FlowClosed
    follows the RPC's event.
  - `travel-close-publish`: `time_travel_reapplies_an_rpc_publication`. The RPC
    `rpc-publish-traveling` commits the same effects while another admitted RPC still holds the
    close. A TimeTravel to the time of the pending close then reapplies the RPC and records the
    close.
- The full workload's other scenarios do not run; a `FullOpenRuns` struct and a dead
  `run_full_workload_after_snapshot` keep their code compiling.

The `pre_rpc_publication_rejection_log` test of the `format_compat` target, in
`crates/dexzd-engine/tests/format_compat/pre_rpc_publication_rejection_log.rs`, checks that the
log holds both RPC commands and the TimeTravel without the rule. It replays the log from the
snapshot and from an empty store, and compares the committed state with `state.txt` row for row:
Channel messages, Attributes, request deduplication replies, and history with its FlowClosed
events.

It is a test fixture, not a `v<N>` version: the generator never writes it and the version pins do
not cover it. Never edit or regenerate it.
