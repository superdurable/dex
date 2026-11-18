---
section: Changed
---
- A fixed pull Worker with **maxConcurrentTasks** unset now opens one stream first, learns the Server's **matching.maxConcurrentTasksCap** from its registration, and clamps its CPU-count default to the cap with a warning instead of failing to start. Its other streams open after that first registration. An explicit **maxConcurrentTasks** above the cap still fails to start, and a fixed Worker with an explicit value still opens all its streams at once.