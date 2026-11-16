---
section: Changed
---
- The Python agents packages and CLI commands depend on **dex-python-sdk**
  1.45.4, the TypeScript ones depend on, and peer-depend on,
  **@superdurable/dex** 1.45.6, and the Go core requires
  **github.com/superdurable/dex/sdk-go** 1.45.6. These releases add the
  graceful completion wait condition.
- The CLI commands download **dexcli** 1.47.9 when no **dexcli** of at least
  that version is found. Its **dexcli dev** runs server-zd 0.1.34, the first
  server with the graceful completion wait condition.
