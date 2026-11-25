---
section: Changed
---
- A Step's default lock fields come from the **getStepOptions** call that **Registry** makes, and **RPCOptions.lockAttributes** is copied when the RPC is declared. The locks Dex acquires then always match the locks the Worker checks.
