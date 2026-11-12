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
  their side effects follow the tool's retry policy. Every call of such a
  node with a wrapper besides the bare deferral, as in every `create_agent`
  graph, stores the non-message graph state with its record, so N calls of
  one model response store it N times. Pass
  `should_defer_tool_calls_before_middleware=False` to keep the earlier
  order. Each turn keeps the order its first model call used, recorded in its
  run state and call records, so a Worker with the other setting finishes it
  without running a wrapper twice; turns paused before this release keep the
  middleware order. A tool node without wrappers, a tool node in a subgraph,
  and a node with only synchronous wrappers are left unchanged. A tool name
  that root tool nodes with different wrappers share now raises `ValueError`
  when the runner loads the graph.
- Python LangGraph adapter: a tool Step that runs a tool node's wrappers no
  longer applies the node's `handle_tool_errors`, so an exception in the tool
  body, such as a `ValidationError`, fails the attempt and a write records
  `outcome_unknown`, as it does without wrappers.
- Python LangGraph adapter: an `InjectedStore` parameter of a tool receives
  the compiled graph's store in its tool Step.
