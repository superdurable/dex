---
section: Changed
---
- Python LangGraph adapter, behavior change: `should_defer_tool_calls_before_middleware`
  now defaults to `True` in `LangGraphAgentRunner`,
  `langchain_agent_session_flow`, and `langgraph_agent_session_flow`. In a
  session the deferral is the outermost tool wrapper of every root graph tool
  node that has an async tool wrapper, whatever the middleware order, and
  each tool Step attempt runs the node's other middleware tool wrappers around
  the tool. Middleware listed before `DurableToolCallMiddleware` no longer see
  calls in `RunModel`, and middleware listed after it now run in the tool Step;
  their side effects follow the tool's retry policy. A call of a node with
  wrappers besides `DurableToolCallMiddleware` stores the non-message graph
  state with its record, so N calls of one model response store it N times.
  Pass `should_defer_tool_calls_before_middleware=False` to keep the earlier
  order. Sessions recorded under either setting continue under the other.
  A tool node without wrappers is left unchanged, so its tools are still
  refused in `RunModel`.
- Python LangGraph adapter: an `InjectedStore` parameter of a tool receives
  the compiled graph's store in its tool Step.
