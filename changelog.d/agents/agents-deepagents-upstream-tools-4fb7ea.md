---
section: Changed
---
- **dex-agents-deepagents** keeps Deep Agents' own file tools, **execute**, **write_todos**, and **task**, on the real workspace files, with the Connector, web fetch, and browser tools on top. Connector writes always wait for approval. File tools stay inside the workspace, also through links. **task** runs its subagent inside one tool Step and waits for approval whenever **approval.always** names any Deep Agents tool. A Connector tool whose name a Deep Agents tool has gets the prefix **connector_**. Large tool results go to a private directory of each session. [Reference](/reference/agents/cli#harness-tools).
- Python LangGraph adapter: with **should_defer_tool_calls_before_middleware**, each tool Step attempt runs the tool node's middleware tool wrappers, such as Deep Agents' large-result eviction, and the tool runtime has the graph's store.
