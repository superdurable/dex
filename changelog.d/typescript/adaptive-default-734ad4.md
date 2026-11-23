---
section: Changed
---
- Pull Workers are adaptive by default. **maxConcurrentTasks** now defaults to 100 instead of the CPU count. An unset **minOpenSlots** now defaults to min(T, max(R + 1, CPU count)) instead of T, so an idle Worker still holds about the CPU count of streams.
- An unset **maxConcurrentTasks** above the Server cap is clamped with a warning, and the **minOpenSlots** and **reservedRpcTasks** defaults come from the clamped value. A Worker whose defaults may drop with the clamp opens one stream first.
- Migration: to keep a Worker fixed, set **minOpenSlots** equal to **maxConcurrentTasks**. To keep the old default slot count, set **maxConcurrentTasks** to the CPU count.
