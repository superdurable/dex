---
section: Added
---
- The Go core's **TurnCompleted.TurnError** ends a turn as failed without a retry, as **turn_error** in Python and **turnError** in TypeScript do: the session stores the turn's messages and usage, sets **last_turn_error**, and writes **turn_failed** instead of **turn_completed**.
