---
section: Fixed
---
- A Go session at the server's **limits.maxFlowStateBytes** now ends with the reason, status **failed**, and a **turn_failed** event, as Python and TypeScript sessions do, instead of failing every later turn with the same error.
