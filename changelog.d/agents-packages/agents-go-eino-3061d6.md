---
section: Eino adapter
---
- New Go module `github.com/superdurable/dex/agents/go/eino` (package
  `dexeino`) runs CloudWeGo Eino ADK `ChatModelAgent`s on the Go core's
  session Flow. Pass the `*adk.ChatModelAgentConfig`, or an
  `AgentConfigBuilder`, to `NewAgentSessionFlow`. See
  [go/eino](go/eino/README.md).
- Each `RunModel` builds a fresh Eino agent over the stored messages and
  stops it after one model call, so the current deploy's instruction and
  tools apply to every model call.
- Each tool call runs in its own Dex tool Step through a one-call
  `compose.ToolsNode` with the agent's tool middlewares and Handler wrappers,
  where `compose.GetToolCallID` and `ToolCallContextFrom` work.
- Every tool needs a Dex effect: `WithToolEffects`, `WithToolPolicies`,
  `ToolEffectDeclarer`, or `WithDefaultToolEffect`.
- Construction refuses `ModelRetryConfig`, `ModelFailoverConfig`, `Exit`,
  `ReturnDirectly`, `ToolsConfig.ExecuteSequentially`, and the
  summarization, automemory, and dream middlewares.
- A stop that a retry would repeat ends the turn as failed with
  `TurnCompleted.TurnError`, with no paid retry: a second model call in one
  Step, a Handler that answers from another model, a turn at
  `MaxIterations`, or an after-model hook that changes the agent state.
- `WithToolSources` offers a tool source's tools, such as
  `connectortools.ConnectorToolSource`, beside the agent's own, and hands
  Eino their input schemas as the source wrote them. Each call runs through
  its source in its tool Step, outside Eino's tool node and middlewares.
- Empty, blank, `/`-containing, and duplicate tool call IDs are replaced,
  and unknown tools are answered as unknown. Model errors reach the core's
  classifier with their HTTP status (`HTTPStatusCode` directly, genai `Code`
  through `ModelAPIError`), so a 429 backs off and a 400 ends the turn.
- It requires `github.com/cloudwego/eino` v0.9.23 or later, tested with
  v0.9.23 and v0.10.0-alpha.35.
