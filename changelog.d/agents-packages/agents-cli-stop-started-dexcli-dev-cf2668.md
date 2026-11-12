---
section: Changed
---
- Python and TypeScript CLI cores (`dex-agents-cli-core`,
  `@superdurable/dex-agents-cli-core`): a `dexcli dev` that a command started
  for `dex.server: auto` now stops when the command exits, instead of running
  on. This covers `/exit`, the end of input, the Ctrl-C that exits, a failure,
  SIGTERM, and SIGHUP, and the `run`, `sessions`, and `connectors`
  subcommands. The command removes `~/.dex/agents/dexcli-dev.json`, sends
  SIGTERM to the `dexcli dev` process group, which includes `dex-server-zd`,
  then SIGKILL after 20 seconds, and prints "Stopped the dexcli dev this
  command started." Another signal during those 20 seconds sends SIGKILL at
  once. The command signals the group only while `dexcli dev` itself still
  runs. After SIGTERM or SIGHUP it exits with 128 plus the signal number. The
  startup line says that the server stops with the command.
- A reused `dexcli dev` is never stopped: one you started, or one that another
  command started. To share one server across sessions, run `dexcli dev`
  yourself first, or set `dex.server`. A command that reused another command's
  `dexcli dev` fails with exit code 1 and one short line when that server
  stops. A command killed with SIGKILL, or one that crashes, leaves its
  `dexcli dev` running; later commands reuse it and do not stop it.
- On Windows, the TypeScript CLI core stops a started `dexcli dev` with
  `taskkill /T`, then `taskkill /T /F`, so `dex-server-zd` stops too. The
  Python CLI core sends CTRL_BREAK_EVENT first, then `taskkill /T /F`. It no
  longer checks the recorded `dexcli dev` with `os.kill(pid, 0)`, which ends
  that process on Windows.
- TypeScript CLI core: during the chat, the Dex SDK Worker's task stream
  reconnect messages go only to `~/.dex/agents/<harness>.log`, so a lost Dex
  server no longer prints stack traces into the chat. Other library warnings
  show their first line on the terminal and their full text in the log file.
- TypeScript CLI core: closing the terminal no longer ends a command on Linux
  with an uncaught `setRawMode EIO` error before it handles SIGHUP. The
  command now stops the `dexcli dev` it started and exits with 129.
