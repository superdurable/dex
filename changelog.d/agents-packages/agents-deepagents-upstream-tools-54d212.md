---
section: Fixed
---
- Python LangGraph adapter: with `should_defer_tool_calls_before_middleware`,
  a tool Step runs the call through its root graph tool node's middleware tool
  wrappers, such as Deep Agents' large-result eviction, and its tool error
  handling, in each attempt; their side effects follow the tool's retry
  policy. Before, the tool Step called the tool alone, so no wrapper ever
  ran. The wrappers see the state of the call's deferral without the
  message list, and their state writes are recorded with the tool's.
  `RunModel` replays the wrapped result without running the wrappers. Every
  call of a root tool node then stores the non-message state in its record.
  Without the option, nothing changes. `dex-agents-langgraph` now requires
  `langgraph` 1.2, `langgraph-prebuilt` 1.1, and `langchain-core` 1.7, the
  tested releases, because it calls a private `ToolNode` method.
- Python LangGraph adapter: a tool's `ToolRuntime` in its tool Step has the
  compiled graph's `store` instead of `None`. It still has no `context`.
