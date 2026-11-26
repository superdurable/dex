---
section: Changed
---
- The timer service wakes for a newly armed timer only when its deadline is earlier than the service's next planned check. This avoids passes that fire nothing when many far-future timers are armed.
