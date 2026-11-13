---
section: Breaking
---
- TypeScript Claude Agent SDK adapter: **createClaudeAgentSessionFlow** now returns an **AgentSessionFlow** typed with **ClaudeAgentRunner**, and the runner's **executeDeferredToolCall** returns a **DeferredToolOutcome**. Code that annotates the Flow as a plain **AgentSessionFlow** no longer compiles. Drop the annotation, or type it with **ClaudeAgentRunner** as the built-in agent loop's Flow is typed with **BuiltinLoopAgentRunner**.
