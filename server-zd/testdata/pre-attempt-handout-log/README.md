# testdata/pre-attempt-handout-log

A copy of `testdata/v1` as the last server-zd binary before Step attempt hand-out recorded it
(commit `9f4344441`). Its heartbeats and Worker results carry no task ID, its attempt failures
carry no guard field, and its Step executions carry no attempt progress.

The `old_log_replay` test in `crates/dexzd-engine/tests/format_compat/` replays it, with and
without its snapshot, and requires the committed state of that binary, except for the Step
execution and run record rows that the newer binary extends. It is a test fixture, not a
`v<N>` version: the generator never writes it and the version pins do not cover it. Never edit
or regenerate it.
