---
section: Added
---
- `dex-agents`, `@superdurable/dex-agents`, and the Go core: the optional
  `sender_name` (`senderName`, `SenderName`) on `AgentUserMessage`,
  `SteerUserMessageRequest`, and `AgentMessage` holds a self-declared display
  name of the person who sent a user message. The model sees the message as
  `"<name>: <text>"` through the framework item, while `AgentMessage.text`
  keeps the text as sent. `send_user_message` and `steer_user_message` return
  false for a name that `is_valid_sender_name` (`isValidSenderName`,
  `IsValidSenderName`) refuses, and for a name sent with `message_id`.
  Encoding omits an absent name, and a Worker before this release ignores it.
- The session graph declares the optional `sender_name` input of the
  `steer_user_message` Action.
- `describe_stored_message` (`describeStoredMessage`), which builds the text
  of a context summary prompt, shows a named user message as
  `user: <name>: <text>`.
