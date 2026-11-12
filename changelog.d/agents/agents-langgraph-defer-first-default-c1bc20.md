---
section: Changed
---
- Python LangGraph adapter, behavior change: middleware tool wrappers run in each tool Step by default, with the deferral as the outermost wrapper whatever the middleware order. **should_defer_tool_calls_before_middleware** now defaults to **True**; pass **False** to keep the earlier order. Each turn keeps the order it started with, so a Worker with the other setting finishes it without running a wrapper twice.
