---
section: Added
---
- Python, TypeScript, and Go cores: **ToolExecutionResult** and **AgentToolCall**
  have an optional `runner_tool_state` (`runnerToolState`, `RunnerToolState`):
  opaque text that a runner returns with a tool's result for its own later
  model calls, such as the framework state the tool changed. The session stores
  it on the `succeeded` record, apart from the output, and returns it to the
  runner in the turn's tool call records. It never reaches the model, the
  browser snapshot (`agent_snapshot_to_json`, `agentSnapshotToJson`), or trace
  attributes. Only the attempt that commits the result stores it. A record
  written before 0.5.0 reads it as absent, and a Worker before 0.5.0 ignores
  it. `agents/conformance` has the field and a `released-0.4.0` tool call
  case.
- Python LangGraph adapter: Deep Agents agents from **create_deep_agent** run
  as agent sessions. A **DeltaChannel** in the root graph is accepted, and each
  turn-local checkpoint stores its whole value. A tool with a **ToolRuntime**
  or **InjectedState** parameter receives the graph state of its deferral in
  its tool Step, without the message list, and reads and writes state as in a
  graph task, as Deep Agents' **StateBackend** file tools do. A tool may return
  a **Command** with an `update` mapping. The tool's state writes travel in
  the result's **runner_tool_state**, and the next `RunModel` replays them
  once. A replayed tool result has the fixed ID
  `dex-tool-result-<turn_id>-<tool_call_id>`. New session Flow and
  **LangGraphAgentRunner** options:
  **excluded_tool_names** removes graph tools from model requests and tool
  Steps, such as `task` to turn off subagents, and frees their names for tool
  sources; **select_turn_chat_model** picks each turn's chat model from its
  **AgentTurnContext**; **should_defer_tool_calls_before_middleware** makes the
  deferral the outermost tool wrapper on the runner's own copy of the graph,
  which `create_deep_agent`'s middleware order otherwise prevents. Existing
  graphs behave as before, except for the fixed ID of a replayed tool result. See
  [A Deep Agents agent](python/langgraph/README.md#a-deep-agents-agent).
- The **dex-agents-deepagents** command uses these options instead of its own
  state schema, subagent middleware replacement, and model middleware. Its
  behavior is unchanged.
