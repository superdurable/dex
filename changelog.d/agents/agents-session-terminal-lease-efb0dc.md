---
section: Fixed
---
- Python and TypeScript CLI commands: when the Dex server cannot reach a
  Worker of the session as the chat starts, such as "Worker stream
  disconnected", the chat's first reads retry for 10 seconds. A Python command
  no longer stops with a traceback: a Dex error that remains prints one line
  and exits with code 1.
