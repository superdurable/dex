---
bump: patch
---
- Ranked events need an explicit arbitration boundary: Channel order ranks only events queued at evaluation; to rank every admitted event, an arbitration Step closes admission under a shared lock and compares consumed and pending messages. Never decide by RPC lock order.
- Locks cover short commits only: no Execute lock on a Step that makes a slow external call, polls, sleeps, or waits long.
- Every external call has its own timeout; the heartbeat timeout covers the longest silent interval with a margin. The code check adds these items and splits the deadline item into separate checks.
- Complete all requested deliverables; pause only the part that needs a missing fact, access, approval, decision, or API. Record routine decisions and keep required approvals.
- SKILL.md adds pattern rows for a bounded concurrency pool and for controlling long-running work, and rules for child Flows that end without reporting, start dedup with a stable Request ID, the partition count constant, and Client use inside a Step.
- The Polling row now forbids polling the provider in WaitFor, consistent with the read-only, time-bounded WaitFor call rule.
