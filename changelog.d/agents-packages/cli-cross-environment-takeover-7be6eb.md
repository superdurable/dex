---
section: Added
---
- `dex-agents-cli-core` and `@superdurable/dex-agents-cli-core`: `--resume`
  takes over a session that another execution environment of the same
  harness started. The session Flow is built with the session's own Flow type
  (`HarnessContext.flow_type` / `flowType`), and the Worker polls that type
  instead of this environment's; it starts polling only once no other
  terminal holds a live lease on the session. A lease holder ID is now
  `<environment Flow type>/<32 hex digits>`. Before each model call, summary,
  tool call, and tool task poll or cancellation, the CLI core reads the stored
  lease from Dex: a Worker of the session's own environment acts unless the
  holder names another environment, and a Worker polling another
  environment's type acts only for the session its process holds. A refused
  tool call records `failed` without running. A takeover from another
  environment refuses and cancels the turn in progress. Leaving another
  environment's session renews the lease for 1 second instead of releasing
  it, so the stored holder keeps naming that environment. In the terminal UI,
  the launch document of such a session leaves out `start_session`. The
  holder ID format, the decision table, and the lines are in the new
  hand-written `agents/conformance/cli_session_environments.json`.
- `--resume` of a session of another harness exits with 2 and a new message:
  `session <id> is a <Flow type> session, which <command> does not run;
  resume it with the command that started it`. It replaces the message that
  told users to resume in the original environment.
