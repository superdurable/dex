# @superdurable/dex-agents-pi-cli

`@superdurable/dex-agents-pi-cli` provides the `dex-agents-pi` command. It runs
a [Pi](https://github.com/earendil-works/pi) coding agent as a durable Dex agent
session and chats with it in your terminal. Dex runs the agent loop: each model
call is one `RunModel` Step through pi-ai, and each tool call is its own Dex
tool Step. The shared runtime,
[`@superdurable/dex-agents-cli`](../runtime/README.md), starts or reuses
`dexcli dev`, connects the connectors MCP server, and runs the Worker. This
package builds the agent with the Pi framework package,
[`@superdurable/dex-agents-pi`](../../../typescript/pi/README.md), and serves
pi-coding-agent's own file and shell tools.

## Running it

The command needs Node.js 22 (22.19 or later) or 24, and the API key of the
model's provider:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
npx -p @superdurable/dex-agents-pi-cli dex-agents-pi
```

The first run asks the `init` questions and writes `~/.dex/agents/pi.yaml`.
Then type a message. Approve or reject tool calls with `y` or `n`, and type
`/exit` to leave. The session stays in Dex, and the command prints the
`--resume` line that continues it. See the
[runtime README](../runtime/README.md) for every subcommand and option, and
[`agents/cli`](../../README.md) for the configuration file.

## Models and API keys

The model is `<provider>/<model>`, as pi-ai's model catalog names them, such as
`anthropic/claude-opus-5-5`, `openai/gpt-5.2`, or
`openrouter/anthropic/claude-sonnet-5`. The text before the first `/` is the
provider. The default is `anthropic/claude-opus-5-5`; set `model` in the file
or pass `--model` to change it. The session settings may pick another model
for later turns.

The command reads only API keys, from the environment, as pi-ai names them per
provider: `ANTHROPIC_API_KEY` for `anthropic`, `OPENAI_API_KEY` for `openai`,
`GEMINI_API_KEY` for `google`, and so on. It never reads Pi's stored logins
(`pi login`) or `ANTHROPIC_OAUTH_TOKEN`, and it never writes a key down.
Providers that pi-ai authenticates with ambient credentials, such as an AWS
profile for Bedrock, need no key. When the file's model has no key, the command
stops before the chat and names the variable. When a turn's session settings
pick a model without a key, that turn fails with the same message.

## Configuration

The `pi` section has three options:

```yaml
model: anthropic/claude-opus-5-5
workspace: ~/dex-agent-workspace
local_tools:
  files: true
  shell: {enabled: true, allow: ["git status", "ls"]}
pi:
  load_project_context: true      # add AGENTS.md files to the instructions
  bash_timeout_seconds: 120       # bash timeout when the model passes none
  provider_base_urls:             # optional: replace a provider's URL
    anthropic: https://llm-proxy.example.com
```

| Field | Default | Meaning |
| --- | --- | --- |
| `pi.load_project_context` | `true` | Whether each model call adds the project context files, as Pi does. See below. |
| `pi.bash_timeout_seconds` | `120` | Seconds a `bash` command runs before it is killed when the model passes no `timeout`, the same default as the runtime's shell tool. A whole number from 1 to 2147483. |
| `pi.provider_base_urls` | `{}` | An `http` or `https` URL per provider, which replaces the URL in pi-ai's catalog, such as a proxy or a compatible endpoint. |

Any other key in the section is a configuration error.

### Project context: AGENTS.md

Like Pi, each model call adds the project's context files after the
instructions:

- `AGENTS.md` in Pi's global directory, `~/.pi/agent` (or
  `PI_CODING_AGENT_DIR`);
- `AGENTS.md` in the workspace and in each directory above it, the outermost
  first.

In each directory, Pi picks the first of `AGENTS.override.md`, `AGENTS.md`,
`AGENTS.MD`, `CLAUDE.md`, and `CLAUDE.MD`. The command reads the files in every
model call on the Worker that runs it, so an edited file applies from the next
model call, and two turns of one session may see different instructions. Set
`pi.load_project_context: false` to add none.

Pi skips a candidate that is not a regular file, without a warning. Each
file it picks is read again with the runtime's workspace file rules, scoped to
the directory it is in. The command pins the real path of each such directory
(the workspace, each directory above it, and Pi's global directory) the first
time it sees it and never derives it again, so a directory later swapped for a
link elsewhere cannot authorize the new place. A file that is a symbolic link
out of its pinned directory, such as `~/.pi/agent/AGENTS.md` linked to a
dotfiles repository, or that has more than one hard link, is skipped, and the
command prints a warning. A link to another file in the same directory, or
below it, loads. This holds whatever `local_tools.files` says.

## Tools and approvals

pi-coding-agent's tools replace the runtime's `files` and `shell` groups. The
runtime's web fetch, browser, and connector tools stay, and the agent can ask
you questions.

| `local_tools` group | Pi tools | Effect | Approval |
| --- | --- | --- | --- |
| `files` | `read`, `grep`, `find`, `ls` | read | No |
| `files` | `write`, `edit` | single-attempt write | Every call |
| `shell` | `bash` | single-attempt write | Unless `local_tools.shell.allow` has the whole command text |

- Each call is one Dex tool Step, recorded in the session. A `write`, `edit`,
  or `bash` call never runs twice. A `write` or `edit` that the workspace file
  layer refuses before it changes anything, such as an outside path, a link, a
  hard link, or an edit conflict, records `failed` and the model sees why. Any
  other failure of a `write` or `edit`, such as one after the rename, records
  `outcome_unknown`. A `bash` call that fails before its shell process starts,
  such as with an invalid `timeout`, records `failed`; one that fails after it
  started, or whose Worker dies while it runs, records `outcome_unknown`.
- Each turn offers only the groups its session settings switch on.
  `approval.always` adds approval by tool name, such as `read`.
- Every file access of the file tools goes through the runtime's workspace
  file layer (`WorkspaceFiles`, see the
  [runtime README](../runtime/README.md#local-tools)), with the path Pi
  finally opens, including `read`'s fallbacks for curly quotes, Unicode
  normalization, and screenshot file names. So the runtime's rules apply: a
  path that leaves the workspace through `..`, an absolute path, `~`, or a
  symbolic link fails; files open without following a final link and must
  still be the file the path names; a file with more than one hard link is
  refused; `write` and `edit` write a temporary file and rename it over the
  target, and never write through a link; `edit` fails with a conflict when
  the file changed after it read it. The check runs in the tool Step, so a
  `write` or `edit` to an outside path still asks for approval and then
  fails. This is not a sandbox: an approved `bash` command, or any other
  program, can change the workspace while a call runs, and the runtime
  README describes the remaining write race.
- The working directory of every tool is the workspace. A `bash` command
  without a `timeout` argument is killed after `pi.bash_timeout_seconds`, 120
  by default, as the runtime's shell tool. A command killed by its timeout
  records `failed`, and the model sees its output and the timeout.
- The tool Step checks every call again, right before it runs, with the
  configuration of the Worker that runs it and the settings its turn started
  with. A call whose group is off there, or that ran without approval but now
  needs one, records `failed` and does not run.
- `find` and `grep` walk the workspace through the workspace file layer
  instead of running `fd` and `rg`, so the command needs neither and never
  downloads them. They skip `.git` and `node_modules`, never follow links, and
  do not read `.gitignore`. `find` matches Pi's glob rules. `grep` reads each
  regular file through the layer, skipping files with more than one hard link,
  files outside the workspace, FIFOs and other special files, and binary
  files. It takes Pi's options (`pattern`, `path`, `glob`, `ignoreCase`,
  `literal`, `context`, `limit`) and matches with JavaScript regular
  expressions, close to `rg`'s syntax. Matching runs in a worker thread with a
  10-second budget per call, so a pattern that backtracks without end fails
  the call with "the pattern took too long to match" instead of blocking. Both
  are slower than `fd` and `rg` on large trees.
- `grep`'s `glob` follows `rg --glob` where practical: a pattern without `/`
  matches names at any depth, one with `/` matches the path below the search
  directory (a leading `/` anchors it there), a trailing `/` matches only
  directories, and a leading `!` excludes. A matching directory includes or
  excludes its whole subtree. Unlike `rg`, globs match case-sensitively with
  `minimatch` syntax, and only one glob is accepted.
- Images that `read` returns reach the model as a note naming the image type.

The command imports pi-coding-agent's package root, because the package exports
no path for single tools. That import loads Pi's terminal UI library and calls
pi-agent-core's `setDefaultStreamFn`, which this command never uses: pi-ai's
own stream makes every model call.

## Sessions and other machines

`dex.server` may point at a Dex server instead of `dexcli dev`; the runtime
handles the connection. A session belongs to the execution environment that
started it: the runtime names the session Flow type after the host, the user,
the configuration file, and the workspace, and `--resume` works only in that
environment. See
[the runtime README](../runtime/README.md#sessions-belong-to-one-execution-environment).
This command builds its Flow with the runtime's Flow type and checks tool
permissions only through the runtime's helpers, so it follows the runtime's
rules. Model replies are stored in pi-ai's format with the pi-ai version; a
pi-ai that cannot read a stored reply fails the model call with an error that
names both versions.

## Development

`ci.sh` builds the core, the Pi package, and the runtime, which this package
installs from the repository, then typechecks and runs the integration tests
against a private `dexcli dev`:

```bash
cd agents/cli/typescript/pi
DEXCLI=/path/to/dexcli ./ci.sh
```

`dex-server-zd` must sit next to `dexcli`. The tests run the command as a child
process with a private home. pi-ai's Anthropic provider calls
`test/fixtures/mock-messages-api.ts`, a scripted Messages API on 127.0.0.1,
through `pi.provider_base_urls`; a refusing proxy stops any other request.
