---
section: Breaking
---
- `@superdurable/dex-agents-claude-agent-sdk`: `ClaudeAgentRunner` now
  implements `AgentFrameworkRunner<DeferredToolOutcome>`, and its
  `executeDeferredToolCall` returns `Promise<DeferredToolOutcome>` instead of
  `Promise<ToolExecutionResult>`. `createClaudeAgentSessionFlow` returns
  `AgentSessionFlow<ClaudeAgentRunner>` instead of `AgentSessionFlow`, as
  `createBuiltinAgentSessionFlow` returns `AgentSessionFlow<BuiltinLoopAgentRunner>`.
  Code that annotates the Flow as a bare `AgentSessionFlow` no longer
  compiles: drop the annotation, or write
  `AgentSessionFlow<ClaudeAgentRunner>`. A parameter that takes the Flow of
  any adapter can use `AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>`.
  A subclass that overrides `executeDeferredToolCall` declares
  `Promise<DeferredToolOutcome>`, and code that reads `.output` from its
  result first checks that the outcome has no `kind`.
