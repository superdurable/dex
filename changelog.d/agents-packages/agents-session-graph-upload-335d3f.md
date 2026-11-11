---
section: Showing sessions in Dex Web
---
- Python and TypeScript cores: new **session_flow_definition_document**
  (`sessionFlowDefinitionDocument`) returns the session Flow Definition Graph
  of one agent as the SDK's **FlowDefinitionDocument**. Pass it in
  `WorkerOptions(flow_definitions=...)` (`flowDefinitions`), and the Worker
  uploads the graph when it starts, so Dex Web shows the agent's sessions and
  Actions without `--flow-rendering-dir`. The upload needs server-zd 0.1.26 or
  later; an older server only logs a warning. Rendering a file with
  **render_session_flow_graph** stays for older servers.
- The document's `generated_at` is a fixed time, the time the graph template
  last changed (`SESSION_FLOW_GRAPH_GENERATED_AT` in
  `dex_agents.session_graph`), the same in both cores. A Worker restart writes
  nothing, and an upgrade replaces the graph. The server keeps a stored graph
  whose time is the same or later, so changing only the Action permission
  needs one upload with `force`. The conformance file
  `session_graph_renderings.json` records the time and the template hash, and
  `generate.py` refuses a template change without a later time.
- Python and TypeScript CLI cores: the command uploads the graph of its
  suffixed session Flow type when it starts, so CLI sessions appear in Dex Web
  with `dexcli dev` 1.45.0. The outcome and any failure go to
  `~/.dex/agents/<harness>.log`, not the terminal. The session Actions require `dex-agents-cli.operate`
  (**SESSION_ACTION_PERMISSION**, `sessionActionPermission`) in every CLI
  core; the conformance file `cli_session_flow_types.json` fixes it.
- Go core: no session graph yet.
