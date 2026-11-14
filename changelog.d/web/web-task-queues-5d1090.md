---
section: Added
---
- A run's details show its **Task queue**, or **default** when the Flow runs on the default task queue. The Flow type is shown on its own row; the Flow type is no longer shown as the task queue.
- A Step attempt's routing shows the **Task queue** of its dispatch next to the serving Worker ID and the queue source. The queue source that was called Task queue is now **Flow type queue**. An RPC shows the Flow's task queue.
- **Start Flow** takes an optional **Task queue**. Leave it empty for the default task queue. Dex Web checks the name before it starts the Flow.
