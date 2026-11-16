---
section: Added
---
- **Condition::graceful_completion_requested** waits until the Flow has a graceful completion request, from **Client::stop_flow** with **StopFlowOptions::graceful_complete** or from a Step's **graceful_complete** decision. **Context::is_graceful_completion_condition_completed** reports it in **execute**. A Wait accepts at most one such condition. It needs Dex Server 0.1.34 or newer; with an older Server the Step's **wait_for** fails with **GracefulCompletionConditionUnsupportedError**. The SDK now supports Server protocol 7.
