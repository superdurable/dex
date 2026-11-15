---
section: Fixed
---
- `ConnectorToolSource` keeps a Connector branch `outcome_unknown` when a
  `sendCount` on an earlier attempt's fence marker counts a send that may have
  taken effect, in both cores. It also does so when an earlier attempt's
  claim stands but the operation returned before it read that record.
- `ConnectorToolSource` records `outcome_unknown` instead of `failed` when a
  later attempt refuses the call's arguments, through the input schema, the
  input validator, or input decoding, while an earlier attempt's checkpoint
  holds a claim or a counted send of the same call. A first attempt, or one
  without such evidence, still records the argument failure.
- `@superdurable/dex-agents`: the TypeScript core reads the `sendCount` that a
  single-dispatch fence with a send cap writes. With
  `@superdurable/connectors-sdk` 0.11, outside the 0.6.11 peer range, 0.6.11
  did not treat such a claim as a fence marker, so a later optional branch
  could record `failed`. The Python core already recognized the claim.
