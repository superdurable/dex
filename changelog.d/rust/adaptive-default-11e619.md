---
section: Changed
---
- Pull Workers are adaptive by default. **max_concurrent_tasks** now defaults to 100 instead of the CPU count, and **min_open_slots** defaults to min(**max_concurrent_tasks**, max(**reserved_rpc_tasks** + 1, CPU count)).
- An idle Worker keeps about the CPU count of slot streams open and grows up to the ceiling under load. A Worker that sets only **max_concurrent_tasks** is adaptive too.
- A ceiling above the Server's **matching.maxConcurrentTasksCap** is clamped with a WARN log; defaults use the clamped value. An adaptive Worker with unset **min_open_slots**, or a fixed Worker with unset **max_concurrent_tasks**, opens one stream to learn the cap.
- Migration: set **min_open_slots** equal to **max_concurrent_tasks** to keep a fixed Worker. Set **max_concurrent_tasks** to the CPU count to keep the earlier default slot count.
