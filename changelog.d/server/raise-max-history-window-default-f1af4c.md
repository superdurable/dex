---
section: Changed
---
- **history.maxWindowEvents** defaults to 1000000000 instead of 100000. A long-lived Flow run can set a much larger **history_window_events** without a server configuration change. The default window, **history.windowEvents**, is unchanged. [Reference](/reference/server/configuration#history-window)
- A Flow run's **history_window_events** of 0 keeps its whole history, under any **history.maxWindowEvents**. The server records it as 2147483647, which older servers also never prune. [Reference](/reference/server/configuration#history-window)
- **history.windowEvents** of 0 keeps the whole history of runs without their own window, and **history.maxWindowEvents** of 0 sets no limit. Older servers fail to start with either at 0. [Upgrades](/reference/server/upgrades)
