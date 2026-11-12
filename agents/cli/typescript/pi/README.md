# @superdurable/dex-agents-pi-cli

`@superdurable/dex-agents-pi-cli` provides the `dex-agents-pi` command. It runs
a [Pi](https://github.com/earendil-works/pi) coding agent as a durable Dex agent
session and chats with it in your terminal. Dex runs the agent loop: each model
call is one `RunModel` Step through pi-ai, and each tool call is its own Dex
tool Step. The shared CLI core,
[`@superdurable/dex-agents-cli-core`](../core/README.md), starts or reuses
`dexcli dev`, connects the connectors MCP server, and runs the Worker. This
package builds the agent with the Pi framework package,
[`@superdurable/dex-agents-pi`](../../../typescript/pi/README.md), and serves
pi-coding-agent's own file and shell tools, with Pi's behavior, in the
directory you start it in.

## Running it

The command needs Node.js 22 (22.19 or later) or 24, and the API key of the
model's provider:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
cd ~/my-project
npx -p @superdurable/dex-agents-pi-cli dex-agents-pi
```

As in Pi, the workspace is the directory the command starts in, unless the
configuration file sets `workspace`. The session Flow type hashes the
workspace, so each directory has its own sessions: `sessions` lists, and
`--resume` continues, only the sessions started in the current directory.

The first run asks the `init` questions and writes `~/.dex/agents/pi.yaml`.
Its workspace question defaults to the launch directory, which writes no
`workspace` field.
Then type a message. Approve or reject tool calls with `y` or `n`, and type
`/exit` to leave. The session stays in Dex, and the command prints the
`--resume` line that continues it. See the
[CLI core README](../core/README.md) for every subcommand and option, and
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
pick a model without a key, that turn fails with the same message. pi-ai
treats an `ANTHROPIC_API_KEY` that starts with `sk-ant-oat` as an OAuth token
and switches to its OAuth mode; use a real API key.

## Configuration

The `pi` section has three options:

```yaml
model: anthropic/claude-opus-5-5
local_tools:
  files: true
  shell: {enabled: true}
approval:
  always: [bash]                  # optional: make every bash call wait
pi:
  load_project_context: true      # add AGENTS.md files to the instructions
  bash_timeout_seconds: 120       # bash timeout when the model passes none
  provider_base_urls:             # optional: replace a provider's URL
    anthropic: https://llm-proxy.example.com
```

| Field | Default | Meaning |
| --- | --- | --- |
| `pi.load_project_context` | `true` | Whether each model call adds the project context files, as Pi does. See below. |
| `pi.bash_timeout_seconds` | `120` | Seconds a `bash` command runs before it is killed when the model passes no `timeout`. A whole number from 1 to 600. Pi itself has no default timeout, because you can abort a command there; cancelling a Dex turn waits for a running tool call, so this command keeps one. |
| `pi.provider_base_urls` | `{}` | An `http` or `https` URL per provider, which replaces the URL in pi-ai's catalog, such as a proxy or a compatible endpoint. |

Any other key in the section is a configuration error.

### Project context: AGENTS.md

Like Pi, each model call adds the project's context files after the
instructions:

- `AGENTS.md` in Pi's global directory, `~/.pi/agent` (or
  `PI_CODING_AGENT_DIR`);
- `AGENTS.md` in the workspace and in each directory above it, the outermost
  first.

In each directory, the command picks the first of `AGENTS.override.md`,
`AGENTS.md`, `AGENTS.MD`, `CLAUDE.md`, and `CLAUDE.MD` that is a regular file
or a link to one, in Pi's order; it does not apply Pi's rule for nested git
worktrees. It reads the files in every model call on the Worker that runs it,
so an edited file applies from the next model call, and two turns of one
session may see different instructions. Set `pi.load_project_context: false`
to add none.

Each file is read with the CLI core's workspace file rules, at most 1 MiB,
without blocking. The command pins the real path of each directory (the
workspace, each directory above it, and Pi's global directory) the first time
it sees it and never derives it again, so a directory later swapped for a link
elsewhere cannot authorize the new place. A file loads only when its real path
is directly in its pinned directory: a link to another file in the same
directory loads, but a link elsewhere, even below that directory, such as
`~/.pi/agent/AGENTS.md` linked to a dotfiles repository, is skipped. A file
with more than one hard link is skipped. In a directory above the workspace,
the file, and for a link such as `AGENTS.md -> CLAUDE.md` the link too, must
belong to you or to root, so another user's file or link in a shared directory
such as `/tmp` never reaches the instructions. Each skipped file
prints one warning. This holds whatever `local_tools.files` says.

## Tools and approvals

The agent keeps pi-coding-agent's own tools and their behavior. They replace
the CLI core's `files` and `shell` groups. The CLI core's web fetch, browser,
and connector tools stay on top, and the agent can ask you questions with
`ask_user`.

| `local_tools` group | Pi tools | Effect | Approval |
| --- | --- | --- | --- |
| `files` | `read`, `grep`, `find`, `ls` | read | None, unless `approval.always` names the tool |
| `files` | `write`, `edit` | single-attempt write | None, unless `approval.always` names the tool |
| `shell` | `bash` | single-attempt write | None, unless `approval.always` names it. `local_tools.shell.allow` has no effect. |

- Pi asks for no approval before a tool call, and this command follows it:
  name a tool in `approval.always` to make every call of it wait. Connector
  writes always wait for approval.
- Each call is one Dex tool Step, recorded in the session. Reads retry. A
  `write`, `edit`, or `bash` call never runs twice: a Worker that dies while
  one runs records `outcome_unknown`.
- A `write` or `edit` that fails before it writes the file, such as an `edit`
  whose `oldText` is not in the file, records `failed`, and the model sees
  why. A failure after the write started, including one after `write`
  created a parent directory, records `outcome_unknown`. A `bash`
  call that fails before its shell starts, such as with an invalid `timeout`,
  records `failed`. So does a command that exits with a non-zero code, or that
  its timeout killed: the model sees its output, then
  `Command exited with code N` or `Command timed out after N seconds`, as Pi
  shows it.
- The tools use Pi's own local file and shell operations, with the workspace
  as their working directory. A relative path resolves against the
  workspace. An absolute path or `~` reaches any file you can reach: Pi has no
  workspace boundary, and neither does this command. The workspace is the
  working directory, not a guardrail and not a sandbox. `bash` runs any
  command with your permissions, without approval by default. Use OS-level
  isolation, such as a container, when the agent must not reach other files.
- A `bash` command without a `timeout` argument is killed after
  `pi.bash_timeout_seconds`, 120 by default. Unlike Pi, the command needs a
  default: cancelling a turn waits for a running tool call to end. A
  `timeout` from the model follows Pi's rules. When a command's output is too
  long, the result shows its end and names the temporary file with the full
  output, as Pi does. That file exists only on the machine whose Worker ran
  the command.
- While `bash` runs, its output so far reaches the session as
  `pi.tool_progress` events. The terminal chat shows the result when the
  command ends; it does not print the progress.
- `grep` and `find` run `rg` and `fd`, so `grep` respects `.gitignore`. When
  one is not on `PATH` or in `~/.pi/agent/bin`, Pi downloads it from GitHub
  to `~/.pi/agent/bin` on first use, as Pi does. With `PI_OFFLINE=1`, Pi
  downloads nothing, and the call fails until you install the tool.
- Each turn offers only the groups its session settings switch on.
- The tool Step checks every call again, right before it runs, with the
  configuration of the Worker that runs it and the settings its turn started
  with. A call whose group is off there, or that ran without approval but now
  needs one, records `failed` and does not run.
- A connector tool whose name a Pi tool has, such as `read`, gets the prefix
  `connector_`, as in `connector_read`; `approval.always` names it that way,
  so `read` there makes only Pi's `read` wait. When that name is taken too, the prefix repeats until the name is free. The
  other connector tools keep their names.

The command imports pi-coding-agent's package root, because the package exports
no path for single tools. That import loads Pi's terminal UI library and calls
pi-agent-core's `setDefaultStreamFn`, which this command never uses: pi-ai's
own stream makes every model call.

## Images

`/image <path>` attaches an image file to the next message, and the model sees
it. The command gives the built-in loop the media store of `media.store`, as
the CLI core describes in [Images](../../README.md#images).

`read` of a PNG, JPEG, GIF, or WebP file returns the image too. Its tool
Step reads the file with Pi's `read`, uploads the image to the same media
store, and the next model call sees it after the
result's text, such as `Read image file [image/png]`. Pi may shrink a large
image first. Any other Pi tool that returns images is handled the same way.

- A tool result carries at most 20 images (`maxImagesPerMessage`). Each image
  after those becomes the note `[image omitted: <mime type>]`, and the text
  ends with `(more than 20 images; the rest were omitted)`.
- An image whose type the media store refuses becomes the same note and does
  not count toward the limit. The call still succeeds.
- A failed upload fails the attempt, and `read` retries.
- A model that pi-ai does not list with image input gets pi-ai's placeholder
  text instead of the image.

## Sessions and other machines

`dex.server` may point at a Dex server instead of `dexcli dev`; the CLI core
handles the connection. A session belongs to the execution environment that
started it: the CLI core names the session Flow type after the host, the user,
the configuration file, and the workspace, and `--resume` works only in that
environment. See
[the CLI core README](../core/README.md#sessions-belong-to-one-execution-environment).
This command builds its Flow with the CLI core's Flow type and checks tool
permissions only through the CLI core's helpers, so it follows the CLI core's
rules. Model replies are stored in pi-ai's format with the pi-ai version; a
pi-ai that cannot read a stored reply fails the model call with an error that
names both versions.

## Development

`ci.sh` builds the core, the Pi package, and the CLI core, which this package
installs from the repository, then typechecks and runs the integration tests
against a private `dexcli dev`:

```bash
cd agents/cli/typescript/pi
DEXCLI=/path/to/dexcli ./ci.sh
```

`dex-server-zd` must sit next to `dexcli`. The tests run the command as a child
process with a private home, started in a project directory of that home.
pi-ai's Anthropic provider calls `test/fixtures/mock-messages-api.ts`, a
scripted Messages API on 127.0.0.1, through `pi.provider_base_urls`; a refusing
proxy stops any other request. `test/fixtures/fake-connectors-mcp-server.ts`
serves connector reads and writes, including tools named `read` and
`connector_read`.

The tests set `PI_OFFLINE=1`, so Pi never downloads `rg` or `fd` during a
test. Before the tests, `ci.sh` runs Pi's `grep` and `find` once with
`PI_CODING_AGENT_DIR` set to a temporary directory. When `PATH` lacks a tool,
Pi downloads it there, and the `grep` and `find` test copies it into the
private home's `~/.pi/agent/bin`. Set `DEX_AGENTS_PI_SEARCH_TOOLS_DIR` to keep
that directory between runs. Agents CI installs `ripgrep` and `fd-find` with
apt instead and sets `PI_OFFLINE=1`, so a missing tool fails `ci.sh` rather
than downloading. The test without `rg` and `fd` hides them by replacing each
`PATH` directory that has one with a directory of links to its other entries.
