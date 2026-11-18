---
section: Added
---
- **PullWorkerOptions.Builder.minOpenSlots** makes a pull Worker adaptive when it is below **maxConcurrentTasks**. The Worker keeps between the two counts of slot streams open, both including the reserved RPC slots: it doubles its Step-and-RPC streams when a task takes the last idle one, and every 30 seconds it half-closes half of the idle surplus. An idle adaptive Worker holds only its floor of Server streams. The default equals **maxConcurrentTasks**, which keeps the Worker fixed with unchanged behavior.
- An adaptive Worker clamps **maxConcurrentTasks** above the Server's **matching.maxConcurrentTasksCap** to the cap with a warning. It treats a **RESOURCE_EXHAUSTED** registration of a growth stream, or of a reconnect after readiness, as a soft limit that it retries at most once per 30 seconds, without a **PullRegistrationError**.
- **Worker.getPullSlotStreamState** returns a **PullSlotStreamState** snapshot of the slot streams: open, registering, busy, and idle streams, orphaned handlers, the floor and capacities, and grow and shrink counters.
