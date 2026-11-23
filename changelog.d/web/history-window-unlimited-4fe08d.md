---
section: Changed
---
- On server-zd 0.1.38 and later, the run's Retention window shows Whole history for an unlimited window, and its editor accepts 0. Older servers keep the numeric window and positive input. [Reference](/reference/server/configuration#history-window)
- The Retention window editor warns that a finite window may delete older events of a run on the server default window, instead of comparing it with a fixed 10000. [Reference](/reference/server/configuration#history-window)
