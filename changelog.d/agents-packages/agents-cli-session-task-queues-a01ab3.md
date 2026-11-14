---
section: Changed
---
- The TypeScript agents packages and CLI commands depend on, and peer-depend
  on, `@superdurable/dex` 1.45.2, so an application can use
  `PullWorkerOptions.taskQueue` and `StartFlowOptions.taskQueue` from that
  release.
- The CLI commands download dexcli 1.47.5 when **DEXCLI** names none.
- `dex-agents-cli-core` and `@superdurable/dex-agents-cli-core`: after a
  terminal UI session switch, the launcher's chat launch document carries the
  new session's own `flow_type` and no `previous_session_id`, and its notices
  carry the takeover lines. The terminal UI handoff needs the
  `launcher_session_switch` capability in `dexcli agents-tui --describe`;
  without it the command uses the plain chat.
