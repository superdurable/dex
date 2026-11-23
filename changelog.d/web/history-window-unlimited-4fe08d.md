---
section: Changed
---
- When the server reports a release of server-zd 0.1.38 or later, the run's Retention window shows Whole history for an unlimited window, and its editor accepts 0. Otherwise it keeps the numeric window and positive input. [Reference](/reference/server/configuration#history-window)
- The Retention window editor warns that a smaller window may delete older events after the next event, also for a run on the server default window, instead of comparing it with a fixed 10000. [Reference](/reference/server/configuration#history-window)
