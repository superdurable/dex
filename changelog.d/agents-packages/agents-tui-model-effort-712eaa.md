---
section: Added
---
- `dex-agents-cli-core` and `@superdurable/dex-agents-cli-core`: the
  `reasoning_effort` session setting sets the reasoning effort of every model
  call from the next turn. A harness declares the values it maps with the
  optional `supported_reasoning_efforts` (`supportedReasoningEfforts`), and the
  turn's value is `ResolvedTurn.reasoning_effort` (`reasoningEffort`). A value
  the harness does not declare fails the turn with
  `unsupported_reasoning_effort`. A harness may also list
  `suggested_models` (`suggestedModels`).
- `resolve_turn_config` (`resolveTurnConfig`) takes the supported values and
  returns the turn's `reasoning_effort`; `TurnConfigResolver` takes them at
  construction. Both additions are optional.
- Terminal UI contract 1: the launch document carries the optional
  `display.models` and `capabilities.reasoning_effort`, which dexcli 1.47.0
  reads for `/model` and `/effort`. Older dexcli releases ignore them.
- Harness commands: `dex-agents-claude-sdk` maps `reasoning_effort` to the
  Claude Agent SDK `effort` (`low`, `medium`, `high`, `xhigh`, `max`). The
  built-in commands, `dex-agents-openai-sdk`, `dex-agents-pydantic-ai`,
  `dex-agents-vercel-ai`, and `dex-agents-pi` map `low`, `medium`, and `high`
  to their SDK's reasoning option. `dex-agents-deepagents`,
  `dex-agents-google-adk`, and `dex-agents-mastra` declare none yet. Every
  command suggests a few models for `/model`.
