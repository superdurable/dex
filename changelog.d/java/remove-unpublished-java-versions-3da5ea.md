---
section: Added
---
- **GracefulCompletion.requested()** waits for a Flow’s graceful completion request. **Context.isGracefulCompletionConditionCompleted()** reports whether the Flow has one. Requires Server protocol 7 (Dex Server 0.1.34+). [Reference](/reference/java/waits#graceful-completion-requests).
- **StopType.GRACEFUL_COMPLETE**, **StopType.FORCE_COMPLETE**, and the **StopFlowOptions(StopType, String, Object)** constructor let **Client.stopFlow** complete a Flow with a result, like the **gracefulComplete** and **forceComplete** Step decisions. They need Dex Server 0.1.34 or newer.
- **Context.supportsGracefulCompletionCondition** uses the negotiated protocol to report whether **waitFor** can use **GracefulCompletion.requested** on the connected Server. The default method returns false. [Reference](/reference/java/waits#graceful-completion-requests).
- **PullWorkerOptions.Builder.minOpenSlots** lets a pull Worker vary open streams with load when set below **maxConcurrentTasks**. The default equals **maxConcurrentTasks**, keeping the stream count fixed. [Reference](/reference/java/workers#capacity-and-shutdown).
- Adaptive Workers warn and clamp **maxConcurrentTasks** when it exceeds **matching.maxConcurrentTasksCap**. Growth and post-readiness reconnects retry after **RESOURCE_EXHAUSTED** without a **PullRegistrationError**. [Reference](/reference/server/workers#adaptive-slot-streams).
- **Worker.getPullSlotStreamState** returns a **PullSlotStreamState** snapshot of the slot streams: open, registering, busy, and idle streams, orphaned handlers, the floor and capacities, and grow and shrink counters.
