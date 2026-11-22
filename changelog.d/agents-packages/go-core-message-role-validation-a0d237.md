---
section: Go core
---
- A message whose role is not `user`, `assistant`, or `tool`, the empty
  string included, in `TurnCompleted.NewMessages` or
  `ToolCallsRequested.NewMessages` now fails the turn at once in
  **RunModel**, with the model call's usage recorded and nothing else of the
  outcome stored, and no retry. Before, the session stored it, and every
  later `get_agent_snapshot` and `list_agent_messages` call failed with
  `MessageRole has unknown value`. The **last_turn_error** names the message,
  such as `TurnCompleted.NewMessages[0] has role "", which is not user,
  assistant, or tool`. The new `MessageRole.IsValid` checks a role, and the
  session never stores a message with another role.
