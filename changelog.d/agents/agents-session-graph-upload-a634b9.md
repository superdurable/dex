---
section: Added
---
- Python and TypeScript cores: **session_flow_definition_document** (**sessionFlowDefinitionDocument**) returns the session graph of one agent as a Flow definition document. Pass it to the Worker, and the Worker uploads the graph when it starts, so Dex Web shows the agent's sessions without a definition directory. The upload needs server-zd 0.1.26 or later. The document's generation time is the time the graph template last changed, so a restart writes nothing and an upgrade replaces the graph. [Reference](/reference/agents/dex-web#upload-the-graph).
- The Dex Agents CLI commands upload the session graph of their session Flow type, so their sessions appear in Dex Web. The session Actions require the permission **dex-agents-cli.operate**.
