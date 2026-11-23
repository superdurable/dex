---
section: Fixed
---
- **dex-agents-claude-agent-sdk** and **@superdurable/dex-agents-claude-agent-sdk**:
  a retried **idempotent_write** whose custom tool a deploy renamed or removed
  now records **outcome_unknown**, as the model already read. Before, its
  record said **succeeded**. (#2201)
- A call that needed approval, or whose first tool Step attempt runs on such a
  Worker, still records **succeeded**, because its tool Step cannot tell it
  from a Claude Code tool. The model still reads **outcome_unknown** for it.
