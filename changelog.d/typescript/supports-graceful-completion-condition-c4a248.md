---
section: Added
---
- **Context.supportsGracefulCompletionCondition** reports whether the connected Server supports **GracefulCompletion.requested**, from the protocol version the Worker negotiated, without a request. **waitFor** can add the condition only when it returns true and keep working with Servers older than protocol 7. [Reference](/reference/typescript/waits#graceful-completion-requests).
