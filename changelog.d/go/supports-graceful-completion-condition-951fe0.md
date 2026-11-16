---
section: Added
---
- **SupportsGracefulCompletionCondition** reports whether the connected Server supports **GracefulCompletionRequested**, from the protocol version the Worker negotiated, without a request. **WaitFor** can add the condition only when it returns true and keep working with Servers older than protocol 7. [Reference](/reference/go/waits#graceful-completion-requests).
