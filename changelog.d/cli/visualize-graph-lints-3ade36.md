---
section: Added
---
- **dexcli visualize** reports the **no_completion_path** warning for Go and Python sources. It marks a Flow where no reachable decision in the analyzed source completes or fails the Flow. The check runs only when every Step, RPC, and timeout handler returns Dex decisions the analyzer can prove.
