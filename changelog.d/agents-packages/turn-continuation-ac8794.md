---
section: Ending or continuing a built-in loop turn
---
- Python and TypeScript cores: new built-in loop hook
  **AgentDefinition.decide_turn_continuation** (`decideTurnContinuation`). At
  the end of each loop iteration it receives a **TurnContinuationContext**
  (the iteration's model output and tool results) and returns `"end"`,
  `"default"`, or a continuation with a message: **ContinueTurn(message)** in
  Python, `{ action: "continue", message }` (**ContinueTurn**) in TypeScript.
  After tool calls, `"end"` completes the turn in the next `RunModel` Step
  without a model call, so the turn's last message is a tool result. After an
  answer without tool calls, a continuation stores the answer and its message
  as user text, then runs another `RunModel` Step in the same turn; the next
  request ends with that user message, as every provider requires. After tool
  calls, a continuation's message follows the tool results. `"default"` and
  no hook keep the earlier behavior.
- At most **MAX_CONSECUTIVE_TURN_CONTINUATIONS**
  (`maxConsecutiveTurnContinuations`, 8) continuations in a row; the next one
  ends the turn with its answer stored and `last_turn_error` set to
  **TURN_CONTINUATION_LIMIT_ERROR**. A tool call resets the count. An
  exception in the hook, or an invalid result such as a blank message, fails
  the `RunModel` attempt, which the retry policy runs again.
- Runner contract additions: the **TurnContinued** outcome
  (`kind: "turn_continued"`) makes the session store its messages and run
  `RunModel` again, and **TurnCompleted.turn_error** (`turnError`) ends a turn
  as failed without a retry. Existing runners need no change.
- Built-in loop run state gains the key `consecutive_continuations`, written
  only after a continued answer. Earlier Workers ignore it, and sessions
  stored before this release continue unchanged.
- TypeScript core: **convertAgentSnapshotToUIMessages** shows a user message
  between two model calls of one turn, such as a continuation message, as the
  start of the next model call's step instead of a user message, as the UI
  stream does.
- Go core: not supported yet.
