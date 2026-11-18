---
bump: patch
---
- Document that a task slot is served by one stream in both fixed and adaptive Workers. Add the sizing rule: a fixed Worker holds a Server stream for every slot, while an adaptive Worker holds about its floor while idle, so choose adaptive for bursty, I/O-bound load.
