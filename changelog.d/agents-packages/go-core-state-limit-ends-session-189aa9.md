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
  **turn_failed**, and fails the Flow with the detail. Before, a model
  answer over the limit failed only its turn, a tool result over the limit
  became a failed or outcome_unknown call that the model saw, and the
  session went on.
- `AgentSessionClient.GetAgentSnapshot` reads how a failed session's Flow
  closed, as Python's `AgentSessionClient.get_agent_snapshot` and
  TypeScript's `readAgentSnapshot` do. It reports status **failed** and the
  Flow's reason also when the session had no room to store them: when a Step
  without a failure route, such as **AwaitUserMessage**, meets the limit, or
  when the failure's own commit does not fit. `SendUserMessage` to a failed
  session returns the new `AgentSessionFailedError` with the reason, which
  still unwraps to `*dex.FlowNotActiveOrNotFoundError`.
