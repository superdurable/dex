---
section: Added
---
- **dexcli agents-tui** shows the message with which an approved plan starts
  running as the notice "Running plan revision N", not as a user message from
  another client. It hides the notes the session writes for the model after a
  failed, steered, or cancelled turn. It reads **AgentMessage.origin**, which
  dex-agents 0.6.0 sets on these messages.
