---
section: Added
---
- Python and TypeScript cores: the built-in agent loop's new hook **decide_turn_continuation** (**decideTurnContinuation**) decides at the end of each loop iteration whether the turn ends or continues. It returns **end**, **default**, or a **ContinueTurn** with a message. After tool calls, **end** completes the turn without another model call. After an answer, a **ContinueTurn** stores the answer and its message as user text and calls the model again in the same turn, so the next request ends with a user message. At most 8 continuations in a row may follow answers without tool calls; the next one ends the turn with an error. The Go core does not support the hook yet.
- Runner contract: the new **TurnContinued** outcome makes the session store its messages and run **RunModel** again, and **TurnCompleted.turn_error** (**turnError**) ends a turn as failed without a retry. Existing runners need no change.
