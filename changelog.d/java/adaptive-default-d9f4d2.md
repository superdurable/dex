---
section: Changed
---
- Pull Workers are adaptive by default: unset **maxConcurrentTasks** is 100, not the CPU count, and unset **minOpenSlots** is about the CPU count. An idle Worker keeps about the CPU count of streams open. [Reference](/reference/java/workers#capacity-and-shutdown).
- Migration: set **minOpenSlots** equal to **maxConcurrentTasks** to keep a fixed Worker. Set **maxConcurrentTasks** to the CPU count to keep the earlier default slot count.
