---
section: Added
---
- `dex-agents` and `@superdurable/dex-agents`: the session Flow registers the
  `SessionLastTerminalEnvironment` Attribute, whose value type
  `SessionTerminalEnvironment` is a map of text pairs.
  `AcquireSessionTerminalLeaseRequest` has an optional `environment` map,
  left out of the wire value when absent. When `acquire_session_terminal_lease`
  gives the lease to a holder that did not hold it, it stores that map in the
  Attribute. A renewal, a refused call, and a release leave it unchanged, so it
  says where the session was last open. A Worker of an earlier release ignores
  the field. The Go core declares the Attribute and the field, and
  `remotesession` declares the Attribute. Names and golden values are in
  `agents/conformance/names.json` and `values/`.
