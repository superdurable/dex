---
section: Added
---
- **timers.fullScanInterval** (default 1h, 0 off) sets how often timer services rescan all due timers. The first pass tracks up to 16 due timers below the old cursor, minus retried ones, and logs an ERROR when no arm notification arrives in 10s. [Reference](/reference/server/configuration#timers)
