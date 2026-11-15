---
section: Added
---
- `FlowErrorType.SUB_FLOW_ABANDONED` marks a failed SubFlow result whose parent Flow stopped waiting for the child, because a Time Travel's SubFlow reset policy failed the restored SubFlow condition. Earlier versions report such a result with a `null` error type. Code that matches every `FlowErrorType` value exhaustively, such as a switch expression, must handle the new value.
