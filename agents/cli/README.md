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
| [`TERMINAL_UI_CONTRACT.md`](TERMINAL_UI_CONTRACT.md) | Contract 1 between a command and the `dexcli agents-tui` terminal UI, with its schemas and conformance cases |

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
| `dex.auth.mode` | `none` | How the Worker and the Client authenticate to the Dex server: `none`, `api_key`, or `bearer`. Any other mode than `none` needs a `dex.server` URL. See A remote Dex server below. |
| `dex.auth.token_url`, `dex.auth.server_id` | none | `api_key` only. The token endpoint that exchanges the key in `DEX_API_KEY`, and the server ID the access tokens are scoped to. `DEX_TOKEN_URL` and `DEX_SERVER_ID` override them. |
| `dex.auth.source`, `dex.auth.file` | none | `bearer` only. `source: env` takes the token from `DEX_CREDENTIAL`; `source: file` reads it from `file`, which `DEX_CREDENTIAL_FILE` overrides. |
| `dex.tls.ca_file`, `dex.tls.server_name` | none | An `https` `dex.server` only. A PEM file of root CAs to trust as well, and the name the certificate must match. `DEX_TLS_CA_FILE` and `DEX_TLS_SERVER_NAME` override them. |
| `connectors.mcp` | `auto` | The connectors MCP server. `auto` uses the one `dexcli dev` provides. Otherwise an `http` or `https` URL on `127.0.0.1`, `localhost`, or `[::1]`. See URLs below. |
| `connectors.enabled` | every authorized connection | Which connections, and which of their operations, the agent may call. See below. |
| `mcp_servers` | `{}` | Remote MCP servers whose tools the agent may call, by name, over Streamable HTTP. See Remote MCP servers below. |
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
Connector credentials stay in the connections you authorize in Dex Web.
Remote MCP server headers take their secrets from environment variables. The
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

### A remote Dex server

```yaml
dex:
  server: https://dex.example.com
  auth:
    mode: api_key
    token_url: https://auth.example.com/oauth/token
    server_id: dex-prod
  tls:
    ca_file: ~/certs/dex-ca.pem    # Only for a private CA.
```

Only `dex.auth.mode` turns authentication on. Without `dex.auth`, or with
`mode: none`, the command reads none of `DEX_API_KEY`, `DEX_TOKEN_URL`,
`DEX_SERVER_ID`, `DEX_CREDENTIAL`, and `DEX_CREDENTIAL_FILE`, so variables
exported for `dexcli` never change a configuration that does not ask for
them. `dex.auth` with `dex.server: auto` is an error (`auth_needs_server_url`):
the local `dexcli dev` takes no credential.

| Mode | Secret | Other settings |
| --- | --- | --- |
| `api_key` | `DEX_API_KEY` | `token_url` (or `DEX_TOKEN_URL`) and `server_id` (or `DEX_SERVER_ID`), both required |
| `bearer`, `source: env` | `DEX_CREDENTIAL` | none |
| `bearer`, `source: file` | the content of `file` (or `DEX_CREDENTIAL_FILE`) | none |

The secret never goes in the file: the schema rejects `api_key`, `token`,
`credential`, and `access_token` under `dex.auth`. Each variable is trimmed,
and one that is empty after trimming is not set. A variable that is set
overrides the file's field of the selected mode, so one file can point at
another token endpoint or server ID per shell. `token_url`, and
`DEX_TOKEN_URL`, must be `https`, or `http` on `localhost`, `127.0.0.0/8`, or
`[::1]` (`insecure_token_url`). A credential may also go to an `http`
`dex.server`; it is then readable on the network path.

An `https` `dex.server` turns TLS on with the default trust store. `dex.tls`
adds a CA bundle or another server name. It needs an `https` server
(`tls_needs_https_server`). `DEX_TLS_CA_FILE` and `DEX_TLS_SERVER_NAME`
override it, and the command reads them only for an `https` server. The URL
scheme decides TLS, so `DEX_TLS` is never read, and neither is
`DEX_FLOW_SERVICE_ADDRESS`. Paths in the file expand a leading `~` and are
relative to the file's directory; paths in a variable expand `~` and are
relative to the working directory.

A missing setting stops the command at startup with exit code 2 and a message
that names the setting, such as
`DEX_API_KEY is not set; dex.auth mode api_key needs it.` A credential that
the token endpoint or the server rejects stops it with exit code 1 and a line
that says which, with the HTTP status or the gRPC status code but not the
remote error text, which may echo the credential. Shell commands, browsers,
downloads, and `dexcli dev` never get `DEX_API_KEY`, `DEX_CREDENTIAL`, or
`DEX_CREDENTIAL_FILE`, whether or not `dex.auth` is set. The terminal UI gets the same `dex.tls` and `dex.auth` in its
launch document and only the one variable its mode needs: see the
[terminal UI contract](TERMINAL_UI_CONTRACT.md#environment).

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

### Remote MCP servers

`mcp_servers` adds the tools of MCP servers that are not Dex connectors, such
as a team's own tool server. Each key is the server's name, and each server
speaks the Streamable HTTP transport. Stateless servers work: every request
opens its own connection and protocol session.

```yaml
mcp_servers:
  studio:
    url: https://mcp.example.com/mcp
    headers:
      Authorization: "Bearer ${STUDIO_API_KEY}"
  local-notes:
    url: http://127.0.0.1:9000/mcp
    trust_tool_annotations: true
```

| Field | Default | Meaning |
| --- | --- | --- |
| name (the key) | | 1 to 32 ASCII letters, digits, `_`, or `-`, starting with a letter. |
| `url` | required | The server's endpoint: `https`, or `http` on `localhost`, `127.0.0.0/8`, or `[::1]` (`insecure_mcp_server_url`). No user info or fragment. It follows the URL rules of `dex.server` and is used as written. |
| `headers` | `{}` | HTTP headers sent with every request to the server. |
| `trust_tool_annotations` | `false` | Whether the server's `readOnlyHint` and `idempotentHint` annotations, and the effects it declares under the Dex MCP conventions, pick each tool's effect. |

The agent sees each tool as `<server name>__<tool name>`, such as
`studio__search_assets`, and `approval.always` names it that way. With
`trust_tool_annotations: false`, every call waits for approval and runs at
most once. With `true`, a tool the server marks read-only or idempotent runs
without approval, unless `approval.always` names it, and may be retried; set
it only for a server whose hints you trust. `dex-agents-pi` and
`dex-agents-deepagents` still make every call that is not a read wait for
approval, as they do for connector tools.

Secrets belong in environment variables, never in the file. In a header
value, each `${NAME}` is replaced by the value of the environment variable
`NAME` when the command starts. `NAME` has ASCII letters, digits, and `_`, and
does not start with a digit. Any other `$` stays as written. The value is used
exactly as set, never trimmed or expanded again. `DEX_API_KEY`, `DEX_CREDENTIAL`, and `DEX_CREDENTIAL_FILE` count as not set,
so a Dex credential never reaches another server. A variable that is not set
or is empty stops the command with exit code 2 and a message that names it,
such as
`mcp_servers.studio.headers.Authorization needs the environment variable STUDIO_API_KEY, which is not set or is empty.`
A value outside printable ASCII stops it the same way. A header value may
also be literal text, but a literal secret then sits in the file: use a
`${NAME}` reference for every secret. Only the Worker expands the values. The command never prints, logs, or stores them:
the effective configuration, the session, and the log file keep the text as
written. When a server echoes a header value, such as in an HTTP 401 body or a
tool error, the command replaces each header value, each of its parts between
spaces, `=`, `:`, `,`, or `;`, and each variable value of at least 8
characters with `[redacted]` in the log file, the terminal, tool output, and
errors. A Worker that runs a stored call checks it against its own
configuration: for a server whose annotations it does not trust, the call must
have had approval and a single-attempt effect, or it fails unsent.

A server that cannot be listed offers no tools until it answers again; the
log file has the error. Session settings cannot change `mcp_servers`.

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
in **Working as** to send a session messages and to answer its approvals and
questions in Dex Web. The terminal UI shows a message from Dex Web marked
`(another client)` and asks about its approvals too. The plain chat follows
its own turns, and may also follow a turn that starts right after one of
them. For a turn that starts while the plain chat waits for input, answer its
approvals in Dex Web. A message from Dex Web runs the command's local tools
like your own, and Dex Web can answer their approvals, so only let people who
may run tools on this machine reach a shared Dex Web.
Uploading needs server-zd 0.1.26 or later, which `dexcli dev` 1.46.0 runs. The
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
`media`, `mcp_servers`, and the harness sections, which belong to the file only. The command reports each
ignored key as a warning, but the turn still runs. Inside a known field, an
unknown key is an error, so `connectors.mcp` fails.

Settings that fail the schema or widen the file are rejected as a whole: the
turn fails with the error code and the JSON Pointer of the first problem, and
the session keeps its settings until someone fixes them.

## Conformance

[`../conformance/cli_config.json`](../conformance/cli_config.json) holds the
cases every CLI core runs: YAML parsing, valid and invalid files with the
expected effective configuration or error, session settings with the
expected turn configuration and warnings, or error, and, in `dex_connection`,
files and environments with the expected Dex connection settings, or the
setting to fix and its exact message, and, in `mcp_servers`, files and
environments with the expected remote MCP servers and their expanded headers,
or the setting to fix and its exact message. Its `error_codes`,
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

## Terminal UI contract

[`TERMINAL_UI_CONTRACT.md`](TERMINAL_UI_CONTRACT.md) specifies how a command
hands its chat to `dexcli agents-tui`: version negotiation, the launch
document, the channel, local requests, and redaction. Its cases are in
[`../conformance/terminal_ui/`](../conformance/terminal_ui), and
`tests/terminal_ui_contract_host.py` plays either side for tests. No released
command uses it yet.

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

## Terminal UI end-to-end check

[`python/terminal-ui-e2e/`](python/terminal-ui-e2e) runs a command on a
pseudo-terminal and checks what the terminal shows. It uses a `dexcli` with `agents-tui`, a private `dexcli dev`, and the
mock model API, so no provider key is needed.

```bash
DEXCLI=/path/to/dexcli agents/cli/python/terminal-ui-e2e/run-scenarios.sh \
  --command py-openai-sdk --command py-builtin
```

Without `DEXCLI`, the script builds `dexcli` from this checkout, which needs
the Dex Web build in `web/assets/dist`. Agents terminal UI e2e CI runs
`py-openai-sdk` and `ts-openai-sdk` on Linux; the other commands run locally.
