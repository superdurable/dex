---
section: Added
---
- TypeScript CLI core (`@superdurable/dex-agents-cli-core`):
  `AgentHarness.defaultWorkspaceLocation`. With `launch_directory`, the
  workspace is the directory the command starts in when the file sets none,
  and `init` writes no `workspace`. `resolveConfig` takes the harness's
  default workspace as an optional fourth argument.
  `ConnectorsMcpToolSource.deferredToolCall` and `executeDeferredToolCall`
  take an optional name for `approval.always` to match, for a wrapper that
  offers the tool under another name. All additions are optional, so existing
  harnesses build and behave as before.
- `@superdurable/dex-agents-pi-cli`: `PrefixedConnectorToolSource` and
  `collidingConnectorToolPrefix`, which offer the connector tools beside Pi's
  tools.
