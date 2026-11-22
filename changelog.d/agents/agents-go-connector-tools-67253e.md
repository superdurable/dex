---
section: Added
---
- The Go core offers Dex Connector operations to an agent as tools, as the Python and TypeScript cores do. The new package **connectortools** takes an operation's **sdkgo.AgentTool**, and **builtinloop.WithToolSources** offers its tools to a built-in loop agent. Each call runs in its own tool Step under the connector's Call ID and single-dispatch fence. A limited Mutation keeps its send cap on every attempt, and Connector writes of one model response run in the model's order. A call that waits for approval shows its arguments, without an approval display yet.
