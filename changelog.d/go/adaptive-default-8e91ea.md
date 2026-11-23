---
section: Changed
---
- Pull Workers are adaptive by default: **MaxConcurrentTasks** defaults to 100, and **MinOpenSlots** to about the CPU count. A ceiling above the Server cap is clamped with a WARN log. [Workers reference](/reference/go/workers#capacity-and-shutdown).
- With **MinOpenSlots** unset, **Start** opens one slot stream first to learn the Server cap, which adds one round trip.
- Migration: set **MinOpenSlots** equal to **MaxConcurrentTasks** to keep a fixed Worker. Set **MaxConcurrentTasks** to **runtime.NumCPU()** to keep the old default slot count.
