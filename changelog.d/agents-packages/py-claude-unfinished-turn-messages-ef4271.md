---
section: Fixed
---
- `@superdurable/dex-agents-claude-agent-sdk`: the tool results that `buildUnfinishedTurnMessages`
  and `RunModel` build for unfinished calls and for ended turns' open calls now name each call by its
  assistant entry and tool use ID. When a model reuses a tool use ID in a later turn, the two results
  get distinct uuids, so the Claude Code CLI no longer drops the second call and its result from the
  next model input, and a later call left open with a reused ID is still answered. The tool call gate
  no longer treats an earlier turn's result as the answer to a new call with the same ID. Answers
  stored under the earlier uuid scheme still count and are not built again.
