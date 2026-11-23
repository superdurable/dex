---
section: Changed
---
- **maxConcurrentTasks** defaults to 100. Unset **minOpenSlots** defaults to **min(T, max(R + 1, CPU count))**. Workers are adaptive when this floor is below **T**; when the CPU count reaches the ceiling, the floor equals **T** and the Worker stays fixed.
- An unset **maxConcurrentTasks** above the Server cap is clamped with a warning, and the **minOpenSlots** and **reservedRpcTasks** defaults come from the clamped value. A Worker whose defaults may drop with the clamp opens one stream first.
- Migration: to keep a Worker fixed, set **minOpenSlots** equal to **maxConcurrentTasks**. To keep the old default slot count, set **maxConcurrentTasks** to the CPU count.
