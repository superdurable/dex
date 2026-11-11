---
section: Changed
---
- Python and TypeScript CLI cores (`dex-agents-cli-core`,
  `@superdurable/dex-agents-cli-core`): a `dexcli dev` that a command started
  for `dex.server: auto` now stops when the command exits, instead of running
  on. This covers `/exit`, the end of input, the Ctrl-C that exits, a failure,
  SIGTERM, and SIGHUP, and the `run`, `sessions`, and `connectors`
  subcommands. The command sends SIGTERM to the `dexcli dev` process group,
  which includes `dex-server-zd`, then SIGKILL after 20 seconds, removes
  `~/.dex/agents/dexcli-dev.json`, and prints "Stopped the dexcli dev this
  command started." After SIGTERM or SIGHUP it exits with 128 plus the signal
  number. The startup line says that the server stops with the command.
- A reused `dexcli dev` is never stopped: one you started, or one that another
  command started. To share one server across sessions, run `dexcli dev`
  yourself first, or set `dex.server`. A command that reused another command's
  `dexcli dev` fails with exit code 1 and one short line when that server
  stops. A record whose process still runs, such as one left by a crashed
  command, is reused and not stopped.
- TypeScript CLI core: during the chat, `console.warn` and `console.error` of
  the Dex SDK and other libraries go to `~/.dex/agents/<harness>.log`, as the
  Python CLI core's logging does, so a lost Worker task stream no longer
  prints stack traces into the chat.
