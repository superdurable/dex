---
section: Breaking
---
- TypeScript OpenAI Agents adapter and the TypeScript **dex-agents-openai-sdk**:
  a turn that waits on a call of a source tool whose name has `-`, such as one
  waiting for approval, while its Worker upgrades fails when it resumes. Its
  saved run state names the tool with `_`, the name the model saw before this
  release, and the SDK no longer finds that tool. New turns are not affected.
