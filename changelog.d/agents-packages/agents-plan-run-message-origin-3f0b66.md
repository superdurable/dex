---
section: Added
---
- Python, TypeScript, and Go cores: a stored user message that the session
  wrote itself carries the new optional `AgentMessage.origin`, so a client can
  tell it apart from text a person typed. The values are `plan_execution` for
  the message that starts an `execute_plan` turn, and `turn_failure_note`,
  `turn_steered_note`, and `turn_cancelled_note` for the notes that tell the
  model which tool calls of an ended turn ran. The new `AgentMessageOrigin`
  names them. A message that a user sent, and every message stored before
  0.6.0, has none. The wire field is `null` when absent; a Worker before
  0.6.0 ignores it and drops it when it moves the message into an archive
  chunk. Decoders keep a value they do not know, so a later value still
  reads as a session-written message. The Go core decodes the field in
  `remotesession` and sets `turn_failure_note` on its own turn-failure note.
