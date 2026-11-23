---
section: Changed
---
- **history.maxWindowEvents** defaults to 1000000000 instead of 100000. A long-lived Flow run can set a much larger **history_window_events** without a server configuration change. The default window, **history.windowEvents**, is unchanged. [Reference](/reference/server/configuration#history-window)
- A Flow run's **history_window_events** of 0 turns off pruning by event count, under any **history.maxWindowEvents**. It is recorded as 2147483647. Older servers apply that value as a finite window; see the mixed-version rules. [Upgrades](/reference/server/upgrades)
- **history.windowEvents** of 0 turns off pruning by event count for runs without their own window, and **history.maxWindowEvents** of 0 sets no limit. Older servers fail to start with either at 0. [Reference](/reference/server/configuration#history-window)
- A finite window above **history.maxWindowEvents** fails with a message that names 0 and 2147483647 as unlimited; a negative window has its own message. The effective configuration log line reports the three history window settings. [Reference](/reference/server/configuration#history-window)
