---
section: Fixed
---
- **dex-agents-openai-agents** and **@superdurable/dex-agents-openai-agents**:
  a turn no longer fails with "Tool ... not found" when a Worker restarts on
  a deploy that renamed or removed a tool the turn already called. The SDK
  cannot resume such a **RunState**, so the runner rebuilds the turn from the
  session's records: it stores the turn's calls with their recorded results
  as history, and the next model call uses the new tool list. A renamed
  single-attempt call that was in flight keeps its **outcome_unknown** record
  and never runs again.
