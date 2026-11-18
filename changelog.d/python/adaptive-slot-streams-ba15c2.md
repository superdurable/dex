---
section: Changed
---
- A fixed pull Worker with **max_concurrent_tasks** unset now opens one stream first, learns the Server's **matching.maxConcurrentTasksCap** from its registration, and clamps its CPU-count default to the cap with a WARNING log instead of failing to start. Its other streams open after that first registration. An explicit **max_concurrent_tasks** above the cap still fails to start, and a fixed Worker with an explicit value still opens all its streams at once.
- When **reserved_rpc_tasks** is None, it now reserves an RPC-only slot only when both **max_concurrent_tasks** and **min_open_slots** are at least two. With the default **min_open_slots** the reserve is unchanged.
