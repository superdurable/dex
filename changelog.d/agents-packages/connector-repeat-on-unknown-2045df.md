---
section: Added
---
- `dex-agents` and `@superdurable/dex-agents`: the new tool effect
  `bounded_write` (`ToolEffect.BOUNDED_WRITE`) runs a write in `CallTool` with
  retries, waits for approval by default, and records `outcome_unknown` when
  its attempts run out. `connector_tool` and `connectorTool` give it to a
  Connector Mutation whose `dex-connector.json` says
  `agent.repeatOnUnknown: limited` with an `agent.maxSends` from 2 to 10, when
  the operation definition declares the same tier and cap. When a send's
  outcome is unknown, the Connector reads back first and resends at most
  `maxSends` times per call; a definite failure is never resent. The call gets
  the operation's own maximum attempts (3 when they are unlimited), at least
  `maxSends`. Any other `repeatOnUnknown` value, an out-of-range `maxSends`,
  a disagreeing definition, or a manifest without these fields keeps the
  Mutation `non_idempotent_write`, as before. `ConnectorTool.max_sends`
  (`maxSends`) holds the cap. A Worker older than 0.6.12, and an older
  `dexcli agents-tui`, cannot read a session that recorded a `bounded_write`
  call, so upgrade every Worker before offering such a tool.
- `ToolPolicy.max_sends` (`maxSends`), an integer from 1 to 10, replaces a
  `bounded_write` Connector tool's send cap; `1` turns resending off. The call
  keeps the cap it was deferred with on every attempt, in its tool source
  reference, and a raised cap adds its difference from the manifest's cap to
  the call's attempts. Other tools ignore it. TypeScript rejects an
  out-of-range value when the session Flow is built, and Python when the
  `ToolPolicy` is created. The cap reaches the Connector with
  `@superdurable/connectors-sdk` 0.11.0 or `dex-connectors-sdk` 0.10.0; an
  earlier SDK never resends. The TypeScript peer range adds `^0.11.0`, and the
  Python `connectors` extra allows `dex-connectors-sdk` from 0.9.0 to 0.10.x.
- `bounded_write` waits for approval by default in `resolve_tool_policy`,
  `resolveToolPolicy`, the MCP tool sources, and the CLI cores, which rank it
  between `idempotent_write` and `non_idempotent_write`. The Go core decodes
  it and runs it in `CallTool`.
