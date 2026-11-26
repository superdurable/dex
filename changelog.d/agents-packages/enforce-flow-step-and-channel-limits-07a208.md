---
section: Changed
---
- The `send_user_message` (Python) and `sendAgentUserMessage` (TypeScript) docs say that `RESOURCE_EXHAUSTED` also means the session's queue holds `limits.maxChannelMessages` messages, which server-zd now enforces. Nothing is queued; wait until the session takes queued messages, then send again.
