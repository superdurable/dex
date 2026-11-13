---
section: Added
---
- `dex-agents` and `@superdurable/dex-agents`: the session Flow registers the
  `SessionTerminalLease` Attribute and the `acquire_session_terminal_lease`
  and `release_session_terminal_lease` RPCs, with the values
  `SessionTerminalLease`, `AcquireSessionTerminalLeaseRequest`,
  `SessionTerminalLeaseResult`, and `ReleaseSessionTerminalLeaseRequest`.
  Acquire succeeds when the lease is free, held by the same `holder_id`, or
  expired, and otherwise returns the other holder's lease; acquiring again
  renews it. Python's `AgentSessionClient` has
  `acquire_session_terminal_lease` and `release_session_terminal_lease`.
  The Go core declares the Attribute and the values, and `remotesession`
  calls both RPCs. A Worker of an earlier release does not serve them, so call
  them only after every Worker of the Flow type runs 0.6.3. Names and golden
  values are in `agents/conformance/names.json` and `values/`.
- `dex-agents-cli-core` and `@superdurable/dex-agents-cli-core`: a command
  holds the terminal lease of the session it shows, renews it every 5 seconds
  with a 15-second lease, and releases it on exit. A second command waits with
  one status line that updates in place, then takes the session over. The
  lines, spinner, and timing are in `agents/conformance/cli_session_lease.json`.
  `CommandEnvironment.session_lease_ttl_seconds` reads the test override
  `DEX_AGENTS_SESSION_LEASE_TTL_SECONDS`.
- Terminal UI contract 1: the launch document's optional
  `capabilities.launcher_session_switch` and the terminal UI's
  `switch_session` message hand `/sessions` and `/new` back to the command,
  which waits for the session's lease and launches the terminal UI again.
