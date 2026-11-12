---
section: Added
---
- Go core: package `remotesession` calls agent sessions that Python or
  TypeScript Workers serve, from a Go Client that serves none.
  `RemoteSessionFlow` registers every session RPC of those cores with the same
  names, locks, state loads, and wire values, including `answer_question`,
  `cancel_turn`, `steer_user_message`, `delete_queued_message`,
  `execute_plan`, and `send_user_message` with `plan_mode` and `images`, plus
  the `AgentEvents` Stream; `RemoteSessionClient` types the calls. The flow is
  client-only, so a Go Worker refuses it. The Go core adds the wire types
  `ExecutePlanRequest`, `SteerUserMessageRequest`,
  `DeleteQueuedMessageRequest`, `CancelTurnRequest`, and
  `AnswerQuestionRequest`. The Go Worker's session Flow is unchanged.
