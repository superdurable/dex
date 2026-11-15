---
section: Fixed
---
- **dexcli agents-tui**: live output turns off only when the Dex server's Stream Store is disabled. Before, any failed precondition on reading the session's events turned it off for the rest of the chat; now such a failure is retried.
