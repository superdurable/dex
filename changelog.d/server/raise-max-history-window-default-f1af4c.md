---
section: Changed
---
- **history.maxWindowEvents** defaults to 1000000000 instead of 100000. A long-lived Flow run can set a much larger **history_window_events** without a server configuration change. The default window of a run, **history.windowEvents**, is unchanged. [Reference](/reference/server/configuration#history-window)
