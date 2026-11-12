# Dex Agents CLI configuration

The Dex Agents CLI commands run one agent harness each, such as
`dex-agents-claude-sdk`, on your machine. The commands come in Python and
TypeScript. All of them read the same configuration format, defined here, so
one file gives the same agent in either language.

| Path | What it is |
| --- | --- |
| [`config.schema.json`](config.schema.json) | JSON Schema (2020-12) of the configuration file |
| [`session-settings.schema.json`](session-settings.schema.json) | JSON Schema of one session's settings, which override the file |
| [`examples/`](examples) | Example configuration files; the tests validate each one |
| [`../conformance/cli_config.json`](../conformance/cli_config.json) | Cases every CLI core must pass |
| [`tests/`](tests) | The schema and conformance checks, with a test-only reference resolver |

## The configuration file

A command reads `~/.dex/agents/<harness>.yaml`, such as
`~/.dex/agents/claude-sdk.yaml`, unless `--config` names another file. The
file is YAML 1.2: only `true` and `false` are booleans, so `yes` and `off` are
strings and fail the schema. A key repeated in one mapping is an error. An
empty file is the same as `{}`.

```yaml
model: claude-opus-5-5
system_prompt: |
  You are my personal assistant. Ask before posting anything.
dex:
  server: auto
connectors:
  mcp: auto
  enabled:
    - slack/support-slack
    - hackernews
    - gmail/personal:
        tools: [get_message, send_message]
approval:
  always: [slack_post_channel_message]
workspace: ~/dex-agent-workspace
local_tools:
  files: true
  shell: {enabled: true, allow: ["git status", "ls"]}
  web_fetch: true
  browser: true
media:
  store: auto
claude: {}
```

| Field | Default | Meaning |
| --- | --- | --- |
| `model` | the command's default model | Any non-empty model ID in the harness's format, such as `claude-opus-5-5` or `openai/gpt-5-mini`. A command without a default model refuses to run until it is set. |
| `system_prompt` | the harness's instructions | The agent's instructions. |
| `system_prompt_file` | none | A UTF-8 file whose text is the instructions. Use either this or `system_prompt`, not both. |
| `dex.server` | `auto` | `auto` reuses this machine's running `dexcli dev` or starts one. Otherwise an `http` or `https` URL of a Dex server. See URLs below. |
| `connectors.mcp` | `auto` | The connectors MCP server. `auto` uses the one `dexcli dev` provides. Otherwise an `http` or `https` URL on `127.0.0.1`, `localhost`, or `[::1]`. See URLs below. |
| `connectors.enabled` | every authorized connection | Which connections, and which of their operations, the agent may call. See below. |
| `approval.always` | `[]` | Tool names, as the agent sees them, whose every call waits for approval. This adds to what the connectors and local tools already require. |
| `workspace` | `~/dex-agent-workspace`; for `dex-agents-pi`, the directory the command starts in | The only directory the local file tools use, and the working directory of shell commands. |
| `local_tools.files` | `true` | Read, write, list, and search files in `workspace`. Reads run at once; writes wait for approval. |
| `local_tools.shell.enabled` | `true` | Run shell commands in `workspace`. Each command waits for approval unless the allow list has it. |
| `local_tools.shell.allow` | `[]` | Commands that run without approval. A command matches only when its whole text equals an entry. `ls -la` does not match `ls`. |
| `local_tools.web_fetch` | `true` | Fetch web pages. |
| `local_tools.browser` | `true` | Drive a local browser. Reads (navigating and text snapshots) run at once; clicks, form fills, and submits wait for approval. The browser tools take no screenshots. The browser is downloaded on first use. |
| `media.store` | `auto` | Where the session's images are stored. `auto` is the directory `~/.dex/agents/media` on this machine. `s3://<bucket>` or `s3://<bucket>/<prefix>` is an S3 bucket, for Workers on other machines. See Images below. |
| `media.endpoint` | none (AWS S3) | The `http` or `https` URL of an S3-compatible service, such as `http://127.0.0.1:9000`. Only valid with an `s3://` store. It follows the URL rules of `dex.server`. |
| `claude`, `openai`, `pydantic_ai`, `deepagents`, `google_adk`, `vercel_ai`, `mastra`, `pi`, `builtin` | `{}` | Options of one harness. Each command defines its own section's fields and ignores the other sections. |

`dex-agents-deepagents` keeps Deep Agents' own file and shell tools. As in
Deep Agents, they run without approval unless `approval.always` names them;
see [its README](python/deepagents/README.md#deep-agents-tools-in-dex).

Other keys are errors, and `null` is never a valid value: omit the field to
take its default. In `system_prompt_file` and `workspace`, a leading `~` is the
home directory and a relative path is relative to the file's directory. The
command expands them when it uses them.

`dex-agents-pi` keeps Pi's own file and shell tools, which have no workspace
boundary. As in Pi, they run without approval unless `approval.always` names
them; see [its README](typescript/pi/README.md#tools-and-approvals).

Do not put secrets in the file. Provider keys come from the environment.
Connector credentials stay in the connections you authorize in Dex Web. The
schema rejects user info in URLs, but it cannot check the harness sections,
which each harness command defines.

### URLs

`dex.server` and `connectors.mcp` take `auto` or an `http` or `https` URL with
no user info (`user:pass@`). `media.endpoint` takes such a URL, without `auto`. The host is a DNS name whose last label starts
with a letter, a four-part IPv4 address, or a bracketed IPv6 address;
`connectors.mcp` allows only `127.0.0.1`, `localhost`, and `[::1]`. A port is
optional. The schema checks this shape. After the schema, the command rejects
the URL with `invalid_url` when an IPv4 part is over 255 or has a leading
zero (`010` would mean 8 to a WHATWG parser), the IPv6 address is invalid, a DNS label starts with `xn--`, or the port is over 65535. Every URL
that passes both checks parses with a WHATWG URL parser.

### Connectors

Each `connectors.enabled` entry is one of:

- `connector`: every connection of that connector, such as `hackernews`;
- `connector/connection`: one connection, such as `slack/support-slack`;
- a map with one such key and a `tools` list, which limits the operations.

`tools` names each operation by its snake-case name, such as `get_message`, or
its operationId, such as `getMessage`. The two spellings are the same
operation. An operationId converts to snake case as the connectors library's
`lowerCamelToSnake` does: a capital letter starts a new word after a lowercase
letter or a digit, and an acronym stays one word, so `listStoryIDs` is
`list_story_ids` and `getHTTPResponse` is `get_http_response`. The
`operation_names` cases in the conformance file fix the exact rule.
Two entries must not cover the same connection, so `slack` and `slack/work`
cannot appear together. An empty list gives the agent no connector tools.

### Images

In the chat, `/image <path>` attaches an image file to your next message. A
leading `~` is the home directory, and a relative path is relative to the
directory you started the command in. The file must be a regular file of at
most 3,750,000 bytes, in PNG, JPEG, GIF, or WebP. The command checks the size
before it reads the file. It then copies the image into the media store and
shows `[image attached] chart.png (image/png, 48 KB)` or similar. A message
carries at most 20 images. A file that fails a check is not attached, and the
command says why.

Session state keeps only a reference to each image. The bytes stay in the
media store, one copy per distinct image, named by its SHA-256. The store
keeps its own copy, so moving or deleting the original file changes nothing.
Images that connector tools return go to the same store.

- `media.store: auto` keeps images in `~/.dex/agents/media` on this machine.
  Only Workers on this machine can read them.
- `media.store: s3://<bucket>/<prefix>` keeps them in an S3 bucket, for
  Workers on other machines. Credentials come from the environment or the
  standard AWS chain, never from the file. With `media.endpoint`, the
  command addresses the bucket in the URL path, as most S3-compatible
  services need. The S3 store needs the agents library's S3 support:
  `pip install 'dex-agents[s3]'` in Python, the `@aws-sdk/client-s3` package
  in TypeScript. Without it the command stops with a configuration error.

The model sees the images only when the harness's runner takes images. The
built-in, Claude Agent SDK, OpenAI Agents SDK, Pydantic AI, Deep Agents,
Google ADK, Vercel AI SDK, Mastra, and Pi commands do. With another harness, the
command says the message was not sent and removes the attached images; send
the message again without them.

## Sessions in Dex Web

The command uploads the session Flow Definition Graph of its session Flow
type when it starts, so Dex Web shows each session's Steps and its
**Agent session** tab without `--flow-rendering-dir`. The session Actions
require the permission `dex-agents-cli.operate` in every CLI core: choose it
in **Working as** to answer a session's approvals and questions in Dex Web.
Uploading needs server-zd 0.1.26 or later, which `dexcli dev` 1.45.0 runs. The
outcome, and a failure such as on an older server, go to the log file
`~/.dex/agents/<harness>.log`, not the terminal, and the command still starts.

## Session settings

Each session has a settings JSON object of at most 16 KiB, set when it starts
and changed with `update_session_settings`, for example from Dex Web. A CLI
command reads it at the start of every turn. It can change the agent's model
and instructions, and only narrow the rest, because anyone who can edit the
session in Dex Web can edit its settings.

| Field | Rule |
| --- | --- |
| `model` | Replaces the file's model. |
| `system_prompt` | Replaces the file's `system_prompt` or `system_prompt_file`. |
| `connectors.enabled` | Replaces the file's list. Each entry must fall inside one of the file's entries: the same connector, the same connection unless the file's entry names none, and a subset of its operations unless it lists none. When the file omits the list, any list narrows it. |
| `approval.always` | Adds tools to the file's list. |
| `local_tools.files`, `web_fetch`, `browser`, `shell.enabled` | `false` switches the tool off. `true` is allowed only where the file has `true`. |
| `local_tools.shell.allow` | Replaces the file's allow list with a subset of it. |

Other top-level keys are ignored, so the settings can also hold keys that
other readers use. This includes `dex`, `workspace`, `system_prompt_file`,
`media`, and the harness sections, which belong to the file only. The command reports each
ignored key as a warning, but the turn still runs. Inside a known field, an
unknown key is an error, so `connectors.mcp` fails.

Settings that fail the schema or widen the file are rejected as a whole: the
turn fails with the error code and the JSON Pointer of the first problem, and
the session keeps its settings until someone fixes them.

## Conformance

[`../conformance/cli_config.json`](../conformance/cli_config.json) holds the
cases every CLI core runs: YAML parsing, valid and invalid files with the
expected effective configuration or error, and session settings with the
expected turn configuration and warnings, or error. Its `error_codes`,
`warnings`, and `check_order` fields define how errors and warnings are
reported.

Schema validators report errors in different orders, so the contract does not
fix one. Every `schema` case has exactly one violation, and a CLI core passes it
when the expected instance location and keyword are among its validator's
errors. Warnings are sorted by key in Unicode code point order; in
TypeScript, compare code points instead of using the default `sort()`.

`tests/` checks the schemas, the examples, and every case against a test-only
reference resolver. Agents CI runs it through `tests/ci.sh`:

```bash
agents/cli/tests/ci.sh
```

## Local end-to-end check

`dex-agents-cli-e2e` runs every agent CLI command, and the agent harness
examples, against a local mock model API and a local `dexcli dev`. No
provider key is needed. It is not part of CI.

```bash
cd agents/cli/python/testing
uv sync --locked
uv run --frozen dex-agents-cli-e2e                       # local build of this checkout
uv run --frozen dex-agents-cli-e2e --source published    # the published 0.5.0 packages
uv run --frozen dex-agents-cli-e2e --suite commands --only py-openai-sdk,ts-pi --seed-hacker-news
```

- **Builds.** By default it builds each command from `--checkout`, this
  checkout unless set, as the command's `ci.sh` does: `uv sync --locked`
  for Python and `npm ci` and `npm run build` for TypeScript, with the
  agent libraries and CLI core from the same checkout. `--source published`
  installs the release wheels of `--published-version` and the npm packages
  instead.
- **Model API.** `dex-agents-mock-model-api` serves the OpenAI Responses
  and Chat Completions APIs, the Anthropic Messages API, and the Gemini API
  on one port of 127.0.0.1. It accepts any model name. Replies come from a
  JSON or YAML scenario keyed by the turn's prompt and the step within the
  turn, and every request goes to a JSONL log with credentials redacted.
  Each command reaches it through its documented variables:
  `OPENAI_BASE_URL`, `ANTHROPIC_BASE_URL`, `GOOGLE_GEMINI_BASE_URL`, or
  `pi.provider_base_urls`.
- **Command suite.** Each command reads a workspace file, writes a file
  after approval, has a write rejected, runs an allowed shell command and
  one that asks, calls the Hacker News `list_story_ids` connector operation
  when a connection is authorized, exits, and resumes the session. It checks
  the terminal, the workspace, the mock's request log, and the session in
  the Dex server: its namespaced Flow type, messages, tool records, and
  uploaded session graph.
- **Framework suite.** The order support agent of each framework example
  looks up an order, cancels one after approval, and has a cancellation
  rejected in a second session, through the example's HTTP session routes.
- **Dex server.** It starts a private `dexcli dev` on free ports, with its
  own home, zd data directory, and connector configuration directory, and
  stops it at the end. No session or connector call reaches another server.
  `--seed-hacker-news` gives that server a Hacker News connection for the
  connector step; without one, the step is skipped. `--reuse-server
  http://127.0.0.1:8802` uses a running `dexcli dev` instead, with its
  sessions and authorized connections; the run never stops it.

Builds, private homes, terminal transcripts, request logs, and the report
go to `--work-dir`. The exit code is 1 when a step failed.
