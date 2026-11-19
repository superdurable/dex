---
section: Fixed
---
- **dex-agents-claude-agent-sdk** and **@superdurable/dex-agents-claude-agent-sdk**:
  a turn no longer fails, or gives the model a "belongs to a recorded call"
  denial, when a Worker restarts on a deploy that renamed or removed a custom
  tool whose call the turn deferred. The resumed segment serves the old tool
  name replay-only, so the CLI receives the call's recorded result. A renamed
  single-attempt call that was in flight keeps its **outcome_unknown** record
  and never runs again. A retried **idempotent_write** whose tool is gone now
  records **outcome_unknown** instead of reporting the "approved" placeholder
  as its result. The next model call uses the new tool list.
