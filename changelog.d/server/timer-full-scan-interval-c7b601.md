---
section: Added
---
- **timers.fullScanInterval** (default 1h, 0 off) sets how often timers are fully rescanned. Its first pass tracks up to 16 unretried, uncovered due timers below the old cursor and logs ERROR when no covering arm notification is observed within 10s. [Reference](/reference/server/configuration#timers)
