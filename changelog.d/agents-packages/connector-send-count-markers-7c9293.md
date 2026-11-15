---
section: Fixed
---
- `ConnectorToolSource` reads the `sendCount` that a single-dispatch fence
  with a send cap writes on its markers. Earlier releases did not treat such a
  marker as a fence marker, so a claim that carried a send count did not keep
  a later optional branch `outcome_unknown`. A marker that counts a send of an
  earlier attempt now also keeps the outcome unknown.
