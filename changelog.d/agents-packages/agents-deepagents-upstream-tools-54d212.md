---
section: Fixed
---
- Python LangGraph adapter: with `should_defer_tool_calls_before_middleware`,
  a tool Step runs the call through its root graph tool node's middleware tool
  wrappers, such as Deep Agents' large-result eviction, and its tool error
  handling, once. Before, the tool Step called the tool alone, so no wrapper
  ever ran. The wrappers see the state of the call's deferral without the
  message list, and their state writes are recorded with the tool's.
  `RunModel` replays the wrapped result without running the wrappers. Without
  the option, nothing changes.
- Python LangGraph adapter: a tool's `ToolRuntime` in its tool Step has the
  compiled graph's `store` instead of `None`. It still has no `context`.
