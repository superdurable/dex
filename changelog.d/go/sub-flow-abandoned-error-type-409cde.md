---
section: Added
---
- **FlowErrorSubFlowAbandoned** marks a failed SubFlow result whose parent Flow stopped waiting for the child, because a Time Travel's SubFlow reset policy failed the restored SubFlow condition. **Execute** reads it from **SubFlowResult** like any other failed child.
