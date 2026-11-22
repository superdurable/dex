---
section: Eino adapter
---
- New Go module `github.com/superdurable/dex/agents/go/eino` (package
  `dexeino`) runs CloudWeGo Eino ADK `ChatModelAgent`s on the Go core's session
  Flow. Pass the `*adk.ChatModelAgentConfig` you would give
  `adk.NewChatModelAgent`, or an `AgentConfigBuilder` that picks the model from
  the session settings, to `NewAgentSessionFlow`. Each `RunModel` builds a
  fresh Eino agent over the stored messages and stops it after one model call,
  so the instruction and tools of the current deploy apply to every model call.
  Each tool call runs in its own Dex tool Step through a one-call
  `compose.ToolsNode` with the agent's tool middlewares and Handler wrappers,
  where `compose.GetToolCallID` and `ToolCallContextFrom` work. Every tool
  needs a Dex effect (`WithToolEffects`, `WithToolPolicies`,
  `ToolEffectDeclarer`, or `WithDefaultToolEffect`). Construction refuses
  `ModelRetryConfig`, `ModelFailoverConfig`, `Exit`, `ReturnDirectly`, and the
  summarization middleware. Empty, `/`-containing, and duplicate tool call IDs
  are replaced, unknown tools are answered as unknown, and model errors expose
  their HTTP status (`ModelAPIError`) so a 429 backs off and a 400 ends the
  turn. It requires `github.com/cloudwego/eino` v0.9.23 or later, tested with
  v0.9.23 and v0.10.0-alpha.35. See [go/eino](go/eino/README.md).
