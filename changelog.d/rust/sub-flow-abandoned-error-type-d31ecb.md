---
section: Added
---
- **FlowErrorType::SubFlowAbandoned** marks a failed SubFlow result whose parent Flow stopped waiting for the child, because a Time Travel's SubFlow reset policy failed the restored SubFlow condition. Earlier versions report such a result with a **None** error type.
