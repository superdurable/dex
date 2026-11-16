---
section: Added
---
- **graceful_completion_requested** creates a wait Condition that completes once the Flow has a graceful completion request, from **StopType.GRACEFUL_COMPLETE** or a **graceful_complete** Step decision. **Context.is_graceful_completion_condition_completed** reports it in **execute**. In **Wait.any_of** it comes after Timers, Channels, and SubFlows. A Wait accepts at most one. It needs Dex Server 0.1.34 or newer; with an older Server, **wait_for** fails with **GracefulCompletionConditionUnsupportedError**. The SDK now supports Server protocol 7.
