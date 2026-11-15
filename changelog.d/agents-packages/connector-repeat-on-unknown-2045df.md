---
section: Added
---
- `dex-agents` and `@superdurable/dex-agents`: a Connector Mutation whose
  `dex-connector.json` says `agent.repeatOnUnknown: limited` with an
  `agent.maxSends` from 2 to 10, and whose operation definition declares the
  same tier and cap, resends a bounded number of times. Its effect stays
  `non_idempotent_write`, so every released reader still decodes its records.
  Each call pins its send cap in its tool source reference,
  `{"tool": "<name>", "maxSends": <n>}`. With a cap of 2 or more the call runs
  in `CallSingleAttemptTool` with the operation's own maximum attempts (3 when
  they are unlimited), at least the cap. When a send's outcome is unknown, the
  Connector reads back first when the operation supports it, and resends while
  the call's sends stay below the cap; a definite failure is never resent.
  Every attempt passes the pinned cap to the Connector, also after a deploy
  changes or drops the manifest's tier, and a Mutation call without a pinned
  cap never resends. Any other `repeatOnUnknown` value, an out-of-range
  `maxSends`, a disagreeing definition, or a manifest without these fields runs
  the call once, as before. `ConnectorTool.max_sends` (`maxSends`) holds the
  manifest's cap.
- `ToolCallStepOptions.bounded_resend_execute_retry`
  (`boundedResendExecuteRetry`) gives a `non_idempotent_write` call whose tool
  enforces its own send cap more than one attempt in `CallSingleAttemptTool`;
  when the attempts run out, the call records `outcome_unknown`.
- `ToolPolicy.max_sends` (`maxSends`), an integer from 1 to 10, replaces a
  Connector tool's send cap; `1` turns resending off and keeps one attempt. A
  raised cap adds its difference from the manifest's cap to the call's
  attempts. Other tools ignore it. TypeScript rejects an out-of-range value
  when the session Flow is built, and Python when the `ToolPolicy` is created.
  The cap reaches the Connector with `@superdurable/connectors-sdk` 0.11.0 or
  `dex-connectors-sdk` 0.10.0; an earlier SDK never resends. The TypeScript
  peer range adds `^0.11.0`, and the Python `connectors` extra allows
  `dex-connectors-sdk` from 0.9.0 to 0.10.x.
- Enable a `limited` Connector tool only after no Worker before 0.6.12 can run
  the session Flow type, and do not roll back to such a Worker while a bounded
  call is open: a Worker before 0.6.12 that runs a later attempt does not pass
  the pinned cap, so a TypeScript Connector uses its definition's cap there and
  can send more often than the call allows. A cap of 1 is safe on any Worker,
  because the call has only one attempt.
