---
section: Added
---
- Python and TypeScript cores: a tool approval can say what the call acts on and what it does. A tool source returns an **AgentInterruptDisplay** with a target, a summary, and an optional preview, and the session stores it in the new optional **AgentInterrupt.display**, on the call's record, and in the **interrupt_raised** event. **ConnectorToolSource** names the operation, the connection, and short arguments, and shows a credential-like argument as redacted. An approval without a display, such as one a Worker before 0.6.0 raised, has none: show its arguments.
- Python and TypeScript CLI commands: a file write's approval carries a diff against the file as it is when the model asks, a shell command's carries the working directory and the full command, and a Connector tool's carries its operation, connection, and short arguments.
