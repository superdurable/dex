# Terminal UI end-to-end scenarios

These scenarios run an agent CLI command with `DEX_AGENTS_TERMINAL_UI=1` on a
pseudo-terminal. The terminal UI is `dexcli agents-tui`. A pyte screen
renders what the terminal shows, and each check reads lines from it. The
package is never published.

Each run uses its own home and workspace. The model is the mock model API of
[`../testing`](../testing), driven by a scenario of scripted turns. A
scripted step may set `stream_delay_seconds` to stream its text slowly. Most
scenarios share one private `dexcli dev`. The output and dexcli dev
scenarios let the command start its own.

| File | What it checks |
| --- | --- |
| `scenarios/test_launch.py` | The first frame at 80 and 50 columns and with `NO_COLOR`; the plain chat with a `dexcli` that lacks `agents-tui`; the `dexcli dev` a command starts stops when it exits |
| `scenarios/test_turns.py` | A streamed reply with a markdown table; a write approval with its diff, and a rejected write; `ask_user`; queue and steer; Ctrl-C cancel, then draft clear, then exit; resize while streaming |
| `scenarios/test_sessions.py` | `--resume` with its history and `/history`; the `/sessions` picker switching sessions |
| `scenarios/test_isolation.py` | No output of the launcher, the Worker host, their children, a shell tool, or `dexcli dev` reaches the terminal; a killed Worker host is shown and the chat exits with 1 |
| `tests/` | The emulator's additions to pyte |

## Output canaries

The output scenario loads a canary into the command's launcher and Worker
host. A Python command loads `output_canary/python_path/sitecustomize.py`
through `PYTHONPATH`. A TypeScript command loads
`output_canary/node_canary.cjs` through `NODE_OPTIONS`. Once the scenario
creates a trigger file, each process writes canary lines to fds 1 and 2,
and so does a child it starts. The lines must reach the command log and never
the terminal.

## Processes a command starts

While a command runs, its session records every process it starts: the
Worker host, a `dexcli dev` with `dex-server-zd`, and their children. After
the command exits, the scenario waits for each one to exit. If one is still
running, the scenario stops it and fails. It also fails when the ports of a
`dexcli dev` the command started still answer.

## Running

```bash
DEXCLI=/path/to/dexcli ./run-scenarios.sh --command py-openai-sdk --command py-builtin
```

- `--command` picks a command from `../testing`'s command list. Repeat it to
  pick several; the default is `py-openai-sdk`. A scenario that needs a
  feature runs only for commands that have it. `ask_user` needs the builtin
  harness. Slow streams need an OpenAI provider. The approval diff is checked
  only for the CLI core's file tools.
- The TypeScript commands hand their chat to the terminal UI only with the
  TypeScript launcher. Until it is on main, `--command ts-openai-sdk` gets
  the plain chat and fails.
- Builds, homes, terminal transcripts, request logs, and command logs go to
  `--work-dir`. Each terminal transcript is `cases/<scenario>/terminal-N.bin`.
- `DEX_SERVER_ZD_BINARY` picks the `dex-server-zd` that `dexcli dev` runs.

`./ci.sh` checks lint and types and runs `tests/`. It needs no `dexcli`.
