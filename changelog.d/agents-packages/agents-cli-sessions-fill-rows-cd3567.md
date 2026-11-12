---
section: Fixed
---
- The `sessions` subcommand of both CLI cores
  (`@superdurable/dex-agents-cli-core` and `dex-agents-cli-core`) now loads
  pages until it has 250 sessions or Dex has no more, instead of stopping after
  five pages. A page with fewer than 50 sessions no longer shortens the list.
  It sends at most 20 requests, and says that more sessions exist when it
  stops before the end.
- `runAgentCli` of `@superdurable/dex-agents-cli-core` now returns only after
  its output reached stdout and stderr. Before, a harness command that exits
  with `process.exit(await runAgentCli(...))` could lose the end of a long
  `sessions` list written to a pipe, including the `Resume one with` line.
