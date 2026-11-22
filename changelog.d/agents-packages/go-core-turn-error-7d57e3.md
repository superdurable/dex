---
section: Go core
---
- `TurnCompleted.TurnError` ends a turn as failed without a retry, as
  `turn_error` in Python and `turnError` in TypeScript do. In the same commit
  the session stores `NewMessages`, records `Usage`, sets
  `AgentState.LastTurnError` to the text, and writes **turn_failed** instead
  of **turn_completed**; no **RecordTurnFailure** runs. The next turn that
  completes clears `LastTurnError`. A framework adapter uses it for a
  deliberate stop that a retry would repeat. A blank `TurnError` fails the
  turn at once with its usage recorded, as for
  `MissingUnfinishedTurnMessageBuilder`. `TurnContinued` is still not
  supported.
