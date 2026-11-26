---
bump: patch
---
- When one event must win over another that arrives at the same moment, the RPCs only publish and one waiting Step decides; never decide between ranked events in an RPC under a lock.
- Locks cover short commits only: no Execute lock on a Step that calls an external system, polls, sleeps, or races sibling Steps.
- Every external call has its own timeout; the code check adds items for call timeouts, heartbeat timeouts, retry budgets, and lock scope, and splits the deadline item into separate checks.
- A clear build request is delivered complete; the owner review no longer invites stopping early when the owner cannot be asked.
- SKILL.md adds pattern rows for a bounded concurrency pool and for controlling long-running work, and adds rules for child Flows that fail without reporting, Request IDs from the complete request, the partition count constant, and Client use inside a Step.
- The Polling row now forbids polling the provider in WaitFor, consistent with the read-only, time-bounded WaitFor call rule.
