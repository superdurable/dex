---
section: Added
---
- The function **supportsGracefulCompletionCondition** reports whether the connected Server supports **GracefulCompletion.requested**, from the protocol version the Worker negotiated, without a request. **waitFor** can add the condition only when it returns true and keep working with Servers older than protocol 7. It returns false for a Context that Dex did not create. [Reference](/reference/typescript/waits#graceful-completion-requests).
