---
section: Added
---
- **GracefulCompletion.requested** creates a wait condition that completes once the Flow has a graceful completion request, from **Client.stopFlow** with **StopType.GRACEFUL_COMPLETE** or a **gracefulComplete** Step decision. The function **isGracefulCompletionConditionCompleted** reports the result in **execute**; it takes the Context, so the Context interface gains no member. The condition needs Dex Server 0.1.34 or newer; with an older Server the Step's **waitFor** fails with **GracefulCompletionConditionUnsupportedError**. The SDK now supports Server protocol version 7.
