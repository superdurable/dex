---
section: Added
---
- **GracefulCompletion.requested()** creates a wait condition that completes once the Flow has a graceful completion request, from **Client.stopFlow** with **StopType.GRACEFUL_COMPLETE** or from a Step's **gracefulComplete** decision. **Context.isGracefulCompletionConditionCompleted()** reports its result in execute. A Step can wait for its next message or the request with **Wait.anyOf**, handle a consumed message first, and return **deadEnd** otherwise. The condition needs Dex Server 0.1.34 or newer; with an older Server, the Step's waitFor fails with **GracefulCompletionConditionUnsupportedException**. The SDK now supports Server protocol 7.
