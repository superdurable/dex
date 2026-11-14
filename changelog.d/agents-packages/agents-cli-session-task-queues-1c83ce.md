---
section: Added
---
- `dex-agents-cli-core` and `@superdurable/dex-agents-cli-core`: every new
  session has `AgentHarness.flow_type` (`flowType`) itself and its own task
  queue, from `session_task_queue` (`sessionTaskQueue`): `s-` and the first 20
  hexadecimal characters of the SHA-256 of the session ID. The command builds
  the session Flow twice, once with the harness's Flow type and once with the
  environment-hashed one, and runs two Workers: one for the open session's
  task queue, which it starts only after reading that the session's terminal
  lease is free and stops when it releases the lease, and one on the default
  task queue for this environment's earlier sessions. The acquire request
  carries the command's environment. The rules, lines, and app Worker IDs are
  in `agents/conformance/cli_session_task_queues.json`.
