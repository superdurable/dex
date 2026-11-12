---
section: Fixed
---
- Ctrl-C at a configuration question of `init`, or of the first run without a
  configuration file, now quits the CLI commands of both CLI cores
  (`@superdurable/dex-agents-cli-core` and `dex-agents-cli-core`). The command
  prints `Cancelled; no configuration written.`, writes no file, and exits with
  code 130. Before, the TypeScript commands ignored Ctrl-C there, and the Python
  commands ignored it during the first run and printed a traceback in `init`.
- Ctrl-D at a configuration question now cancels the same way and exits with
  code 1. Before, the Python commands took the default answer and went on.
