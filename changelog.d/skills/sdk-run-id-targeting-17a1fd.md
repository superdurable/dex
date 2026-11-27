---
bump: patch
---
- **dex-app-builder**: **core/flow.md** reads, waits for, and stops a claimed child run by its run ID. It removes the describe-before-and-after check and the child stop RPC used to compare run IDs.
- **core/client.md** explains how a run ID selects one retained run. A targeted stop never affects a newer run; a not-found result means the tracked run already stopped.
