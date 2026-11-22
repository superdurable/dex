---
section: Go core
---
- A Go session whose Step commit would pass server-zd's
  `limits.maxFlowStateBytes` now ends, as Python and TypeScript sessions
  do. When **RecordToolFailure**, **RecordToolOutcomeUnknown**, or
  **RecordTurnFailure** gets a `ValueTooLarge` recovery error whose detail
  contains `run state of `, it sets the status to **failed** and
  **last_turn_error** to the detail, clears the turn, **FrameworkRunState**,
  and **PendingInterrupts**, keeps the turn's records, writes
  **turn_failed**, and fails the Flow with the detail. Before, the session
  kept taking messages and every later turn failed with
  `active run state of ... exceeds the maximum of ... bytes`.
