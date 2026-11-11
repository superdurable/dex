---
section: Added
---
- `@superdurable/dex-agents-pi`: a Pi tool's `prepareArguments` becomes the
  function tool's **prepareArguments**, so the built-in loop converts an old
  argument shape before the schema check. A plain error a Pi tool throws
  follows the call's **ToolCallContext.effect**, the effect after
  `toolPolicies`: it fails the call for `read` and `idempotent_write`, and
  records `outcome_unknown` for `non_idempotent_write`, `unclassified`, or a
  context without an effect. See [typescript/pi](typescript/pi/README.md).
