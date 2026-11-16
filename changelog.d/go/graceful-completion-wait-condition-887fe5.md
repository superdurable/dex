---
section: Added
---
- **GracefulCompletionRequested** returns a Condition that completes once the Flow has a graceful completion request, from **Client.StopFlow** with **GracefulCompleteFlow** or a **GracefulComplete** Step decision. **IsGracefulCompletionConditionCompleted** tells **Execute** whether it completed, so a Step that waits for the next message can return **DeadEnd**. It needs Server protocol 7, server-zd 0.1.34 or later; against an older Server, WaitFor fails with **GracefulCompletionConditionUnsupportedError**. The SDK supports Server protocol 7. [Reference](/reference/go/waits#graceful-completion-requests).
