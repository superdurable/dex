---
section: Added
---
- Python, TypeScript, and Go cores: **StopFlow** with graceful complete ends a
  session as **COMPLETED**, from a client or from one of the session's own
  tools. The running turn and the user messages queued before the request
  finish first. It needs server-zd 0.1.34 or later, which **dexcli** 1.47.9
  runs, and every Worker of the session's Flow type on 0.6.14 or later.
  [Reference](/reference/agents/session-limits#end-a-session).
