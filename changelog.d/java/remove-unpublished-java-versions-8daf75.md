---
section: Changed
---
- Fixed pull Workers with unset **maxConcurrentTasks** now warn and clamp the CPU-count default to **matching.maxConcurrentTasksCap**. An explicit limit above the cap still prevents startup. [Reference](/reference/server/workers#adaptive-slot-streams).
