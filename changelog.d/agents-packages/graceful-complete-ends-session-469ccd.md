---
section: Added
---
- **dex-agents**, **@superdurable/dex-agents**, and the Go core: **StopFlow**
  with **GracefulComplete** ends a session as **COMPLETED**, from a client or
  from one of the session's own tools. **AwaitUserMessage** also waits for the
  graceful completion condition when the server supports it. The running turn
  finishes and stores its tool results and final assistant message, user
  messages queued before the request still run their turns, and the session
  Flow then completes. It needs server-zd 0.1.34 or later; with an older
  server the wait stays unchanged. Send graceful complete only after every
  Worker of the session's Flow type runs 0.6.14 or later. The conformance
  suite adds the **graceful_complete** transcript.
