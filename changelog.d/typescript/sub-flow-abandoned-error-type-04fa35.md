---
section: Added
---
- `FlowErrorType.SUB_FLOW_ABANDONED` marks a failed SubFlow result whose parent Flow stopped waiting for the child, because a Time Travel's SubFlow reset policy failed the restored SubFlow condition. `execute` reads it from `SubFlow.getConditionResults` like any other failed child. Code that matches every `FlowErrorType` value exhaustively, such as a `switch` with a `never` check, must handle the new value.
