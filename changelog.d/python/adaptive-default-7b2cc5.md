---
section: Changed
---
- Pull Workers are adaptive by default. **max_concurrent_tasks** defaults to 100, not the CPU count. **min_open_slots** defaults to about the CPU count, not **max_concurrent_tasks**, so setting only **max_concurrent_tasks** is adaptive too. [Reference](/reference/python/workers#capacity-and-shutdown).
- A default ceiling above **matching.maxConcurrentTasksCap** is clamped with a warning, and unset **min_open_slots** and **reserved_rpc_tasks** come from the clamped ceiling. [Reference](/reference/python/workers#capacity-and-shutdown).
- An adaptive Worker with **min_open_slots** unset opens one stream first, learns the cap, and then opens its other floor streams. [Reference](/reference/python/workers#capacity-and-shutdown).
- The synchronous **Worker** handler thread pool allows up to **max_concurrent_tasks** threads, 100 by default, and starts them on demand.
- Migration: set **min_open_slots** equal to **max_concurrent_tasks** to keep a fixed Worker. Set **max_concurrent_tasks** to the CPU count to keep the old default slot count.
