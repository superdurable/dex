---
section: Changed
---
- Python LangGraph adapter, behavior change: middleware tool wrappers run in each tool Step by default, with the deferral as the outermost wrapper whatever the middleware order. **should_defer_tool_calls_before_middleware** now defaults to **True**; pass **False** to keep the earlier order. Sessions recorded under either setting continue under the other.
