---
section: Fixed
---
- **dex-agents-claude-agent-sdk** and **@superdurable/dex-agents-claude-agent-sdk**:
  when one model reply holds two calls that must run outside the turn, the
  model now reads the first call's recorded result in that same reply's
  results. Before, a live model's stream could leave the refused second call
  after the first call's deferral in the transcript, and the resumed CLI showed
  the first call as "[Tool result missing due to internal error]" until the
  model's next such call, so the model could repeat a write that had already
  run. The transcript given to the resumed CLI now puts the pending deferral
  after the reply's refused calls; stored entries do not change. The refusal
  of the second call now says that it was not run because it came in the same
  answer as an earlier call that had not finished, names that call, says its
  result is reported separately, and asks the model to call the refused tool
  again by itself if it is still needed. It no longer implies the earlier call
  succeeded. (#2111)
