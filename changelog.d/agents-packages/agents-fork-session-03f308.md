---
section: Changed
---
- The Python agents packages and CLI commands depend on **dex-python-sdk**
  1.45.3, and the TypeScript ones depend on, and peer-depend on,
  **@superdurable/dex** 1.45.5. Both releases add clone fork, which session
  forks use.
- The CLI commands download **dexcli** 1.47.8 when no **dexcli** of at least
  that version is found. Its **dexcli dev** runs server-zd 0.1.33, the first
  server with clone fork.
