---
section: Changed
---
- **Worker.start** now resolves after one stream of each slot kind has registered, as in the other SDKs, instead of after the first stream of any kind. **waitPullRegistered** is unchanged. This aligns TypeScript readiness with the other SDKs, a behavior change the owner authorized for this release. **start** still rejects with **PullRegistrationError** when every stream of a slot kind is rejected before any of that kind registers.
- A fixed pull Worker with **maxConcurrentTasks** unset now opens one stream first, learns the Server's **matching.maxConcurrentTasksCap** from its registration, and clamps its CPU-count default to the cap with a warning instead of failing to start. Its other streams open after that first registration. An explicit **maxConcurrentTasks** above the cap still fails to start, and a fixed Worker with an explicit value still opens all its streams at once.
- A pull Worker opens its RPC-only streams by slot kind instead of as the last slots, and an unset **reservedRpcTasks** reserves no slot when **minOpenSlots** is 1.
