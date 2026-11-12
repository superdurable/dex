# Dex Agents terminal UI contract, version 1

An agents CLI command, such as `dex-agents-openai-sdk`, can show its chat in a
terminal UI that `dexcli` draws. For that, the command splits into three
processes:

- The **launcher** is the process the user ran. It reads the configuration,
  resolves the Dex server and `dexcli`, starts the other two processes, and
  relays local requests.
- The **Worker host** runs the harness, the local tools, and the Worker. It
  never sees the terminal.
- The **terminal UI** is `dexcli agents-tui`. It owns the terminal and talks to
  the Dex server directly for RPCs, Streams, and searches.

This document is contract 1 between the launcher and the terminal UI. The
launcher and `dexcli` are released separately, so both sides follow the
version rules below. The channel between the launcher and the Worker host is
internal to one CLI core version and is not part of this contract. When the
launcher relays a request to the Worker host, the terminal UI still sees one
peer: "the launcher side" below means the launcher together with any Worker
host it relays to.

Status: this is a specification with conformance assets. No released command
or `dexcli` implements it yet.

| Path | What it is |
| --- | --- |
| [`terminal-ui-launch.schema.json`](terminal-ui-launch.schema.json) | JSON Schema (2020-12) of the launch document |
| [`terminal-ui-channel.schema.json`](terminal-ui-channel.schema.json) | JSON Schema of every later channel message, and each method's params and result |
| [`terminal-ui-describe.schema.json`](terminal-ui-describe.schema.json) | JSON Schema of `dexcli agents-tui --describe` output |
| [`../conformance/terminal_ui/`](../conformance/terminal_ui) | Cases every implementation must pass, and channel transcripts |
| [`tests/terminal_ui_contract_host.py`](tests/terminal_ui_contract_host.py) | Test-only peer that plays either side of a transcript, and reference functions for the rules |
| [`tests/test_terminal_ui_contract.py`](tests/test_terminal_ui_contract.py) | Checks the schemas, the cases, the transcripts, and the host |

Every object in the schemas accepts unknown properties, because receivers
ignore fields that a later addition to contract 1 brings.

The schemas are normative. Their patterns end with `(?![\s\S])`, a lookahead,
because Python's `$` also matches before a trailing newline. ECMAScript
validators such as Ajv, in strict mode, and Python's `jsonschema` read them
as written. Go's `regexp` has no lookahead, so a Go implementation either
validates with a regular expression engine that has one, or checks the same
rules in code. Both are conforming.

## Negotiation

### `--describe`

Before a handoff the launcher runs `dexcli agents-tui --describe`. It needs no
terminal, no network, and no environment. It prints exactly one JSON object
of at most 1 MiB on stdout, followed by a newline, and exits 0. It prints
nothing else on stdout and no update notice anywhere.

```json
{"dexcli_version":"1.46.0",
 "contracts":{"1":{"modes":{"chat":{"uses_methods":["put_image","start_session","list_connectors","open_url","get_request_result"]},
                            "sessions":{"uses_methods":[]},
                            "connectors":{"uses_methods":["list_connectors","open_url"]}},
                   "capabilities":["images","plan_mode","turn_control","reasoning_effort"]}}}
```

- `contracts` is keyed by the contract version as a decimal string without
  leading zeros.
- `modes` lists the modes `dexcli` runs for that contract. `uses_methods` lists
  the local request methods the mode may call. It is informational: the
  terminal UI only calls methods the launch document lists.
- `capabilities` lists the launch document capabilities `dexcli` reads.

### Choosing a contract

The launcher uses the plain text chat, and prints one line that says why,
when any of these holds:

| Plain reason | When |
| --- | --- |
| `describe_failed` | `--describe` does not exit 0 within 5 s, or its stdout is not one UTF-8 JSON object of at most 1 MiB that passes `terminal-ui-describe.schema.json`. An older `dexcli` without `agents-tui` fails here. |
| `no_common_contract` | No contract version is supported by both sides. |
| `mode_missing` | Common contracts exist, but none has the mode this subcommand needs. |

dexcli 1.46.0 is the first release whose `agents-tui` speaks contract 1. The
`describe_failed` line says so and tells the user to point `DEXCLI` at such a
`dexcli`. The `no_common_contract` line says which side to upgrade: the
command when every contract `dexcli` speaks is newer than its own, else
`dexcli`.

Otherwise the launcher takes the contracts both sides support, highest first,
and uses the first one whose `modes` include the needed mode. A missing mode
moves only that subcommand to plain text.

The launcher decides before negotiating that some runs never hand off: Windows
(checked before probing `dexcli`), `--plain`, stdin or stdout that is not a
terminal, `TERM=dumb`, and harnesses that declare plain text only.

### Versioning rules

- `contract` is an integer.
- Adding an optional field, an optional method, an optional capability, a
  message `type`, or an error code keeps the version. Receivers ignore unknown
  fields and unknown message types. An unknown method gets `unknown_method`.
  An unknown error code is shown as a failure of that operation with its
  message.
- The channel schema accepts a message of any type contract 1 does not
  define, as long as `type` is a lowercase name, so an older receiver
  validates and skips what a newer sender adds. A message of a defined type
  must still match that type's definition.
- A new required item, or any change of meaning, needs a new version.
- `dexcli` keeps supporting an old version while any supported agents release
  uses it. Dropping a version is a breaking change.

| Situation | Behavior |
| --- | --- |
| `dexcli` supports 1 and 2, the launcher only 1 | Contract 1 |
| `DEXCLI` points at a `dexcli` without `agents-tui` | `describe_failed`: plain text, with a line that names the version needed |
| `dexcli` lacks a mode | That subcommand uses plain text |
| The terminal UI needs a method the launcher does not list | The terminal UI hides that feature |
| A failure after the handoff and before `ready` | Plain text on the same session (see [Ready](#ready)) |

## Launching

The launcher starts the terminal UI in the foreground process group, on the
user's terminal:

```text
dexcli agents-tui --channel-fd <N>
```

`<N>` is the channel's actual fd number in the child: do not assume 3. Only the
fd number goes on the command line.

### The channel

- The channel is one end of a connected `AF_UNIX` `SOCK_STREAM` socket, such
  as a `socketpair`. Node's `"pipe"` stdio entry on POSIX is such a socket.
- Each socket end is held only by its two processes. `dexcli` sets
  close-on-exec on `<N>` right after opening it. The launcher passes the fd to
  no other child, such as `dexcli dev` or `stty`.
- Either side closing the socket ends the session of the contract.

### The launcher's own output

While the terminal UI runs, the launcher's output goes to the command's log
file instead of the terminal. A Python launcher points fds 1 and 2 at the log
with `dup2` and restores them afterwards. Node has no atomic `dup2`, so a Node
launcher covers its JavaScript output (`process.stdout`, `process.stderr`, and
`console`) and the children it starts, which get a log file, a pipe, or
nothing as stdout and stderr. Its guarantee does not cover raw writes to fds 1
and 2 by native code. The Worker host's stdout and stderr are the log file in
both.

### Environment

The launcher builds the terminal UI's environment from an allow-list and never
passes its own environment through. It copies each of these names that it has:

- `HOME` (only to show paths as `~`), `TERM`, `COLORTERM`, `TERM_PROGRAM`,
  `NO_COLOR`, `LANG`, `LC_ALL`, `LC_CTYPE`, `TZ`, and `DEX_AGENTS_TUI_DEBUG`;
- the one auth variable that `dex.auth` names, if any:

| `dex.auth` | Variable |
| --- | --- |
| `{"mode":"none"}` | none |
| `{"mode":"bearer","source":"env"}` | `DEX_CREDENTIAL`, the token |
| `{"mode":"bearer","source":"file"}` | `DEX_CREDENTIAL_FILE`, the token file's path |
| `{"mode":"api_key","token_url":…,"server_id":…}` | `DEX_API_KEY`, the key |

Nothing else passes: no provider key (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`,
`GOOGLE_API_KEY`, …), no `*_BASE_URL`, no cloud credentials, no connector
variables, and no `PATH`, `DISPLAY`, or `BROWSER` (the launcher opens URLs for
the terminal UI, see `open_url`). `dexcli agents-tui` reads its Dex
connection only from the launch document and that one variable. It ignores
`DEX_TOKEN_URL`, `DEX_SERVER_ID`, `DEX_TLS*`, and any other `DEX_*` variable,
so the launch document's `token_url`, `server_id`, and `tls` win.

### Launch document

The launcher writes the launch document as the first line of the channel,
right after the spawn. It holds no secret.

```json
{"type":"launch","contract":1,"mode":"chat",
 "command":{"name":"dex-agents-openai-sdk","harness":"openai-sdk","language":"python","version":"0.7.0"},
 "dex":{"server_address":"127.0.0.1:8801",
        "tls":{"enabled":false,"ca_file":null,"server_name":null},
        "auth":{"mode":"none"},
        "web_url":"http://127.0.0.1:8802"},
 "session":{"flow_type":"DexAgentsOpenAISdkCliSession-ab12cd","session_id":"openai-sdk-20261111-142233-a1b2c3","is_resumed":false},
 "display":{"model":"gpt-5.2","workspace":"~/dex-agent-workspace","config_path":"~/.dex/agents/openai-sdk.yaml",
            "models":["gpt-5.2","gpt-5-mini","gpt-5-nano"]},
 "methods":["put_image","start_session","list_connectors","open_url","get_request_result"],
 "capabilities":{"images":{"max_per_message":20,"max_bytes":3750000},"plan_mode":false,"turn_control":true,
                 "reasoning_effort":{"values":["low","medium","high"]}},
 "notices":[],
 "log_path":"~/.dex/agents/openai-sdk.log"}
```

| Field | Meaning |
| --- | --- |
| `command` | The executable, its harness name (the prefix of its session IDs), `python` or `typescript`, and the package version. |
| `dex.server_address` | The FlowService `host:port` the launcher resolved. |
| `dex.tls` | `enabled`, plus a CA bundle path and a server name, each `null` for the default. |
| `dex.auth` | How to authenticate, as in the table above. A launch document whose `auth` carries `credential`, `api_key`, `token`, or `access_token` fails the schema. |
| `dex.web_url` | The Dex Web base URL, or `null`. `open_url` only opens URLs under it. |
| `session` | chat: `flow_type`, `session_id`, and `is_resumed` (`true` loads the history). sessions: `flow_type`. |
| `display` | Text to show and never log. Paths write the home directory as `~`. All optional. `models` lists the model IDs `/model` offers: the configured model first, then the harness command's suggestions. See [Model and reasoning effort](#model-and-reasoning-effort). |
| `methods` | The local requests the launcher side serves. |
| `capabilities` | From the session Flow the Worker host actually built, never guessed from the harness name. `images` is present only when the runner accepts images, with its limits. `plan_mode` is `true` only when the Flow was built with plan mode. `turn_control` says whether the Flow supports steer and cancel; `state.accepts_turn_control` in the snapshot still decides each time. `reasoning_effort` is present only when the harness command maps the `reasoning_effort` session setting to its SDK, with the `values` it accepts, from least to most effort. An absent boolean is `false`. |
| `notices` | Notices to show once the first frame is up, in order: the lines the launcher printed while it started a `dexcli dev`, such as its log file, as `info`, then the Worker host's startup notices, such as connector tools that are unavailable. A reused `dexcli dev` adds no line. It is often empty. |
| `log_path` | The command's log file, for display. |
| `connectors` | connectors mode: the rows, in the shape of `list_connectors`'s result. |

Values in the example are illustrations; the CLI core constants decide.

| Mode | The launcher must provide | Optional; without it |
| --- | --- | --- |
| `chat` | `session` (chat form) | `put_image`: no `/image` and no pasted images. `start_session`: no `/new`. `list_connectors`: no `/connectors`. `open_url`: no `/web` or Dex Web key. `get_request_result`: a timed-out request counts as unknown. `display.models`: `/model` offers only `display.model`. `capabilities.reasoning_effort`: `/effort` says the command has no reasoning effort setting. |
| `sessions` | `session.flow_type`; the launcher handles `result` | `open_url` |
| `connectors` | `connectors` | `list_connectors`: no refresh key. `open_url` |

`form` is reserved for a later version and is not a mode of contract 1.

### Launch checks

`dexcli` checks the launch line in this order and stops at the first failure.
It then sends one `launch_rejected` message, writes nothing on the terminal,
leaves the terminal mode as it found it, and exits.

| Check | Code | Exit |
| --- | --- | --- |
| The line is at most 1 MiB, UTF-8 JSON, an object, with `type` `launch` | `invalid_launch` | 2 |
| `contract` is an integer this `dexcli` supports | `contract_unsupported` | 3 |
| `mode` is a mode this `dexcli` runs for that contract | `mode_unsupported` | 3 |
| The document passes the contract's schema | `invalid_launch` | 2 |
| The variable `dex.auth` needs is set and not empty | `auth_missing` | 2 |
| The terminal could be set up (raw mode, window size) | `terminal_failed` | 1 |
| The Dex client could be set up: the credential that `dex.auth` names is usable, `dex.tls.ca_file` is readable | `dex_setup_failed` | 1 |

The last row runs only after every check above it passed, so it never hides
one of them. A launcher treats `dex_setup_failed` like any failed handoff.

```json
{"type":"launch_rejected","code":"auth_missing","message":"DEX_API_KEY is not set; dex.auth mode api_key needs it."}
```

The message is one English sentence for the launcher to print after
`Plain chat:`. It names variables but never holds an environment value or a
secret. `--channel-fd` that is not a socket exits 2 without a message.

### Ready

`ready` means the terminal UI is usable: the launch document passed, the
terminal is set up, and the first interactive frame, with a working input
box, is drawn. `dexcli` then sends `{"type":"ready"}`. It does not wait for a
snapshot or any Dex RPC; it loads the first snapshot afterwards, with its own
retries, and a slow server never causes a fallback.

- The first message `dexcli` sends is `ready` or `launch_rejected`.
- The launcher sends nothing after the launch line until it reads `ready`.
- The launcher allows 5 s from the spawn. All of it is local work.
- A failure before `ready` is a failed handoff: `dexcli` exits, the channel
  ends, 5 s pass, or `launch_rejected` arrives. On a timeout the launcher sends
  SIGTERM, waits 2 s, then sends SIGKILL. It restores the terminal mode it
  saved before the spawn, prints `Plain chat: <reason>.`, and continues in the
  plain text chat on the same session.
- A failure after `ready` never falls back. The launcher restores the
  terminal and exits.
- After `ready`, `dexcli`'s exit summary is the only line that says how to
  resume: `/new` can switch sessions, so the launcher could name the wrong
  one. The launcher prints its own resume line only when it continues in the
  plain chat, or when `dexcli` never sent `ready`.

### When the channel ends

When the launcher reads EOF on the channel, finds a protocol error in
[Framing](#framing), or receives `launch_rejected`, it waits for `dexcli`:

1. It gives `dexcli` 1 s to exit by itself.
2. Then it sends SIGTERM, and SIGKILL 2 s later if `dexcli` still runs.

What follows depends on when it happened:

- Before `ready`, this is a failed handoff (see [Ready](#ready)). The launcher
  continues in the plain text chat on the same session.
- After `ready`, the launcher restores the terminal mode it saved, stops the
  Worker host, prints one line that says why the chat closed, and exits 1. An
  EOF after which `dexcli` exits by itself with 0, 1, or 130 within the 1 s is
  a normal end instead: the launcher prints no such line and exits with
  `dexcli`'s code, such as 1 after a failed turn (see
  [Exit codes](#exit-codes-of-dexcli-agents-tui)). A stopped Worker host still
  makes it exit 1. Any other exit by itself, such as 143 or a signal, ends the
  chat unexpectedly: the launcher prints one line with the exit code and exits
  1. A protocol error stays a failure even when `dexcli` then exits 0.

### SIGINT that reaches only the launcher

A SIGINT sent to the whole foreground process group reaches `dexcli` too, and
`dexcli` exits by itself (130). A SIGINT sent only to the launcher, such as
`kill -INT <launcher pid>`, does not. So the launcher:

1. waits 1 s for `dexcli` to exit by itself;
2. then forwards SIGINT to `dexcli`;
3. sends SIGTERM 2 s after that if `dexcli` still runs;
4. restores the terminal, stops the Worker host, prints how to resume only
   when `dexcli` never sent `ready` (see [Ready](#ready)), and exits 130.

This never falls back to the plain chat, even before `ready`.

### Exit codes of `dexcli agents-tui`

| Exit | When |
| --- | --- |
| 0 | The user exited. |
| 1 | A failure: the session or a turn failed, the channel ended or broke after `ready`, `terminal_failed`, or `dex_setup_failed`. |
| 2 | `invalid_launch`, `auth_missing`, or `--channel-fd` is not a socket. |
| 3 | `contract_unsupported` or `mode_unsupported`. |
| 130 | An external SIGINT, after an orderly exit. A Ctrl-C key press in raw mode is a key, not a signal. |
| 143 | SIGTERM, after restoring the terminal. |

## Framing

- Each message is one line: a UTF-8 JSON object followed by `\n`. Senders
  write compact JSON. A line holds no raw newline.
- A line is at most 1 MiB (1048576 bytes of UTF-8, without the `\n`).
- Every message has a string `type`. A receiver ignores a message whose `type`
  it does not know.
- A line that is too long, not UTF-8 JSON, not an object, or without a string
  `type` is a protocol error. The receiver reads and discards the rest of the
  line. The launch line fails the launch checks above. Any later protocol
  error ends the contract: the receiver closes the channel. `dexcli` then
  restores the terminal and exits 1.
- A message of a known `type` that fails its definition is malformed, not a
  protocol error, and the channel stays open. A malformed `request` whose
  `id` is an integer from 1 to 2^53−1 gets an `invalid_params` response with
  that `id`, and that `id` counts as used. Any other malformed message,
  including a `request` without such an `id` (missing, a string, a
  fraction, or out of range), is dropped without an answer; the receiver
  keeps reading the channel.
- A sender whose peer already closed sees `EPIPE`; that is the end of the
  contract, not a crash.

## Messages

| From | `type` | Fields |
| --- | --- | --- |
| launcher | `launch` | The launch document; first line only. |
| launcher | `response` | `id`, and exactly one of `result` (an object) or `error`. |
| launcher | `notice` | `level` (`info`, `warning`, or `error`) and `text`. |
| launcher | `worker` | `state` (`ready`, `reconnecting`, `stopping`, or `stopped`); `exit_code` only with `stopped`. |
| terminal UI | `ready` | none |
| terminal UI | `launch_rejected` | `code` and `message`. |
| terminal UI | `request` | `id`, `method`, `params` (an object), and `idempotency_key` except for `get_request_result`. |
| terminal UI | `cancel` | `id` |
| terminal UI | `result` | `resume_session_id`; sessions mode only. |

Any other `type` is a later addition: the receiver skips the message,
whatever its fields. A known `type` sent by the wrong side, such as `ready`
from the launcher, is malformed.

An `error` is `{"code":"…","message":"…"}`: a lowercase snake_case code and
one English sentence that holds no secret and no environment value. All
errors use this one shape.

`worker` reports the Worker host's state. `exit_code` is its exit status, or
128 plus the signal number when a signal killed it. After `stopped` the chat
stays open but cannot run turns; `dexcli`'s exit summary says how to resume
after the user exits.

A launcher must send `stopped` when the Worker host exits, and should send
`stopping` when it starts to shut the host down. `reconnecting` and `ready`
are optional: a launcher sends them only when its Worker host can tell that
its Worker lost or regained the Dex server. Until the first `worker` message
the terminal UI treats the Worker as running, and it never waits for
`ready`.

`result` is sent at most once, right before `dexcli` closes the channel and
exits 0. The launcher acts on it only when it read a complete `result` and the
exit code is 0. It then checks the session again with `DescribeFlow` (type and
running) before resuming it.

## Requests

```json
{"type":"request","id":7,"idempotency_key":"01a1cafd-3028-7a3c-9b21-4f6e8d0c2a1b","method":"put_image","params":{"path":"/home/user/pictures/screenshot.png"}}
{"type":"response","id":7,"result":{"image":{…},"name":"screenshot.png"}}
{"type":"response","id":7,"error":{"code":"not_found","message":"…"}}
```

### Request IDs

`id` is 1 for the first request of a `dexcli` process and one more for each
request after it, up to 2^53−1. It only pairs a response with its request.
Every request gets exactly one response.

### Idempotency keys

- Every request except `get_request_result` carries an `idempotency_key`. That
  one takes the key it asks about in `params` instead.
- A key is a lowercase, hyphenated UUIDv7 (RFC 9562), made by `dexcli` for one
  user action. Its time must lie in the years 2000 to 9999. A retry of the
  same action reuses the key; a new action gets a new key.
- The launcher side answers a request with an invalid key with
  `invalid_params`.
- The launcher side remembers each key's request and outcome, in the
  process that handled it, and never longer than that process lives:
  - A key that still runs is never forgotten. The in-flight limit bounds
    how many run.
  - A finished key is forgotten 10 minutes after it finished.
  - When more than 256 keys are kept, finished keys are forgotten earlier,
    the one that finished first first, until 256 remain. Capacity wins over
    the 10 minutes.

  A request whose key it remembers returns the first outcome, after waiting
  for it when the first request still runs. A remembered key with a different
  method or different `params` gets `invalid_params`. A forgotten key is
  handled afresh, which is safe for every contract 1 method.
- Only an outcome of the method itself is remembered: its result or one of
  its own errors. The launcher side never remembers `invalid_params`,
  `unknown_method`, `busy`, `cancelled`, `timeout`, or `host_stopped`. The
  first three are rejections; the last three end a wait, not the operation.
  A retry after any of them is handled afresh, and `get_request_result`
  answers `unknown` or `running` for its key, never `done` with one of these
  codes.
- The table is a convenience. `put_image` and `start_session` stay safe
  without it, because the image is content-addressed and the Dex server
  remembers which key started each session (see `start_session`).

### In-flight limit

At most 4 requests other than `get_request_result` may wait for a response at
once. A fifth gets `busy` right away. `get_request_result` never counts and is
never `busy`.

### Cancel and timeouts

`{"type":"cancel","id":7}` stops only the wait. It never undoes the operation,
which may still finish and be remembered under its key. If the response was
not sent yet, the launcher side answers right away with `cancelled`;
otherwise it ignores the cancel. Either way the request has exactly one
response.

`dexcli` waits for each method at most:

| Method | Wait |
| --- | --- |
| `put_image` | 30 s |
| `start_session` | 30 s |
| `list_connectors` | 15 s |
| `open_url` | 5 s |
| `get_request_result` | 5 s |

When the wait ends, `dexcli` sends `cancel` and treats the outcome as not
known. The launcher side may also answer `timeout` itself when its relay
gives up, which `dexcli` treats the same way. `list_connectors` and
`open_url` then show the failure, and the user can retry with a new action.
`put_image` and `start_session` recover like this:

1. When the launch document lists `get_request_result`, `dexcli` asks with
   the same key.
   - `done` gives the outcome.
   - `running`: ask again 1 s after each answer. `start_session` shows
     `· still starting…`. Stop asking 60 s after the original request was
     sent; the outcome stays unknown, so go to step 3.
   - `unknown` (the key was forgotten, for example after a Worker host
     restart): go to step 2.
   - No answer within 5 s (send `cancel`) or `timeout`: go to step 2.
   - `unknown_method`: stop calling `get_request_result` for the rest of the
     process and go to step 2.
2. Without a usable `get_request_result`, `dexcli` sends the original
   request again, once, with the same key. A second run creates nothing new:
   `put_image` is content-addressed, and `start_session` finds the session
   its key already started. When that request also ends without an outcome,
   go to step 3.
3. When the outcome is still unknown: `put_image` attaches nothing and shows
   `! Could not attach <name>; try again.` `start_session` shows
   `! Could not confirm the new session; check /sessions.`

Losing the Worker host ends recovery. The launcher side answers every waiting
and every later relayed request with `host_stopped`, and sends
`{"type":"worker","state":"stopped",…}`. `dexcli` does not resend after
`host_stopped`: the image counts as not attached, and `start_session` shows
the message of step 3. A channel that ended has the same effect.

A user who presses Esc while `start_session` waits only moves the wait to the
background: no `cancel` is sent. When the result arrives, the terminal UI asks
`Started session <id>. Switch to it? y switch · n stay` and never switches
silently.

### Methods

| Method | Served by | Params | Result | Errors |
| --- | --- | --- | --- | --- |
| `put_image` | Worker host | `{path}`, absolute | `{image, name}` | `not_found`, `too_large`, `not_an_image`, `store_failed` |
| `start_session` | Worker host | `{}` | `{session_id}` | `start_failed`, `id_conflict` |
| `list_connectors` | Worker host (chat), launcher (connectors) | `{}` | `{mcp_url, tools, connections?}` | `mcp_unavailable` |
| `open_url` | launcher | `{url}` | `{}` | `refused`, `browser_off`, `open_failed` |
| `get_request_result` | the side that served the key | `{idempotency_key}` | `{state, result?, error?}` | none of its own |

Common errors of every method: `unknown_method`, `invalid_params`, `timeout`,
`busy`, `cancelled`, and `host_stopped` (the Worker host is gone, so a relayed
request cannot finish).

When `dexcli` gets `unknown_method`, it turns that feature off in place and
shows one line such as `! /image is not available with this command version.`
The chat goes on.

**`put_image`** reads the file, checks its size and type, and writes it to the
command's media store, addressed by the SHA-256 of its bytes, so running it
twice gives the same reference. `image` is the `AgentImageReference` to put
unchanged into `AgentUserMessage.images`. `name` is the file's base name for
the attachment chip. Limits come from `capabilities.images` and the image
rules in [`../conformance/images.json`](../conformance/images.json). A
cancelled `put_image` attaches nothing; a stored file without a reference is
harmless.

**`start_session`** starts a new session of the launch document's Flow type
and returns its ID once the session is ready, as the command does at startup.

1. It derives two candidate session IDs from the key:
   `<harness>-<YYYYMMDD>-<HHMMSS>-<hex6>` and the same with `<hex12>`. The
   date and time are the key's UUIDv7 milliseconds, floored to the second, in
   UTC; this is the format of every CLI session ID. `hex6` and `hex12` are the
   first 6 and 12 lowercase hex digits of the SHA-256 of the key's 36 ASCII
   bytes. `harness` is `command.harness`. The vectors in
   [`../conformance/terminal_ui/session_ids.json`](../conformance/terminal_ui/session_ids.json)
   fix this.
2. Every `StartFlow` call below passes the key as the start request's
   `request_id` and sets "ignore already started" (`request_id` and
   `ignore_already_started` in the Dex SDKs' start options). The Dex server
   stores the request ID with the run. For a Flow ID whose latest retained
   run this key started, `StartFlow` returns that run and starts nothing.
   For a Flow ID another request started, it fails with `FlowAlreadyStarted`.
3. It reads the long candidate with `DescribeFlow`. When that Flow exists, it
   calls `StartFlow` on it. A returned run is this key's earlier session:
   the result is the long ID, and nothing else is tried. This step comes
   first so that a resend never starts the short ID while the long one
   already holds this key, for example after the run that once held the
   short ID was purged.
4. It calls `StartFlow` on the short candidate. A returned or new run gives
   the short ID.
5. When the short candidate fails with `FlowAlreadyStarted` (a 6-digit
   suffix collision, about 1 in 16 million for two keys of the same second,
   or a session the plain chat started with a random suffix), and step 3
   found no long Flow, it calls `StartFlow` on the long candidate. A
   returned or new run gives the long ID.
6. Otherwise it answers `id_conflict`, and the terminal UI shows
   `! Session ID conflict; press /new again.` The next `/new` is a new action
   with a new key.

Within one Worker host the key table runs one request per key at a time, so
no second request with the same key races these steps.

**What is guaranteed.** A resend with the same key never starts a second
session, and never takes another request's session, while the Dex server
still retains the runs that key may have started. This holds after the key
table forgot the key and after a Worker host restart. Resends happen only
inside a request's recovery (at most 60 s after it was first sent, see
[Cancel and timeouts](#cancel-and-timeouts)), far shorter than any server
retention. After the server purged the key's own run, a later resend of that
key may start a new session; this is outside the guarantee.

`session_ids.json` has two keys that share a 6-digit ID, the outcome for each
set of retained Flows, and two sequences with purges: the other key's run is
purged and the fallback key resends (its long-ID run again, nothing new), and
the fallback key's own run is purged and it resends (a new session). Neither
the session start input nor any Flow Attribute changes: the key lives in the
server's start request record.

**`list_connectors`** returns what `connectors` shows: `mcp_url`, one `tools`
row per connector tool (`name`, `connector`, `connection`, `operation`, each
`null` without a Dex identity, and `is_enabled` from `connectors.enabled`), and
`connections`, the Dex Web connections (`connector`, `connection`, `status`),
or `null` or absent when the launcher did not read them.

**`open_url`** opens a Dex Web page in the user's browser. It refuses, with
`refused`, any URL that is not under `dex.web_url`:

- a URL with anything but printable ASCII (U+0021 to U+007E), or with a
  backslash. The terminal UI percent-encodes what it puts into a path.
- a `web_url` of `null`;
- a scheme other than `http` or `https`, or one that differs from `web_url`'s
  (compared without case);
- an authority with userinfo (`user@`) or a percent sign;
- a host that is not a bracketed IPv6 address or ASCII letters, digits, dots,
  and hyphens, or that differs from `web_url`'s without case. There is no IDNA
  conversion: a Unicode host never equals its `xn--` form;
- a port that is empty, has a leading zero, is not decimal, or is outside 1
  to 65535, or that differs from `web_url`'s (80 for `http` and 443 for
  `https` when absent);
- a `.` or `..` path segment, also written with `%2e`;
- a path that is not `web_url`'s path without trailing slashes, or that path
  followed by `/` and more. An empty base path allows every path. An encoded
  slash (`%2F`) does not end a segment.

The check covers the URL it is given. Where a Dex Web page sends the browser
next is up to Dex Web.

A URL that passes these checks is opened, and the result is `{}`. When the
command's browser opening is turned off (`DEX_AGENTS_OPEN_BROWSER=0`), the
launcher opens nothing and answers `browser_off`. It answers `open_failed`
when the system's opener (such as `open` or `xdg-open`) fails to start, or
exits non-zero within 4 s. An opener still running after 4 s counts as
opened. `refused` is checked first, so a URL outside `web_url` is
`refused` even while browser opening is off. Running it twice is harmless.

**`get_request_result`** reads the outcome remembered for a key. `done` holds
exactly one of `result` or `error`, as the original response would. `running`
and `unknown` hold neither. A `cancelled` response is not an outcome: once the
operation finishes, `get_request_result` returns its real outcome.

## Model and reasoning effort

`/model` and `/effort` change the session's settings, not the configuration
file. The terminal UI reads the session's settings from its snapshot, sets
`model` or `reasoning_effort` in them, and writes them back with the session's
`update_session_settings` RPC on the Dex server. No local request is involved.
The new value applies from the next turn, and `--resume` keeps it, because
the session stores its settings.

- `/model` with no argument offers `display.models`, with the current model
  marked. The current model is the session settings' `model`, else
  `display.model`. `/model <ID>` takes any ID without spaces, listed or not.
- `/effort` with no argument offers `capabilities.reasoning_effort.values`
  and `default`, with the current value marked. `default` removes
  `reasoning_effort` from the settings, so the model's own default applies.
  `/effort <value>` takes only one of these.
- `reasoning_effort` is a session setting of
  [`session-settings.schema.json`](session-settings.schema.json). A Worker
  rejects a value its harness command does not declare, and the turn fails.

dexcli 1.47.0 is the first release that reads `display.models` and
`capabilities.reasoning_effort`. Older releases ignore them, as they ignore
every unknown field. A launcher that sends neither, such as one of agents
0.6.0, gets a `/model` picker with `display.model` only, and `/effort` says
the command has no reasoning effort setting. A Worker of agents 0.6.0 ignores
a `reasoning_effort` setting and reports it as an unknown key.

## Notices

The launcher turns the Worker host's user-facing log lines into `notice`
messages. Startup notices, such as `Not authorized yet: …`, go into the launch
document's `notices` instead of the terminal, because the terminal UI covers
the screen right after the handoff. Progress lines that the launcher prints
while it waits, such as for a `dexcli dev` it starts, stay in the scrollback
too.

Both the launch document's `notices` and `notice` messages are shown like
this:

1. Every control character (U+0000 to U+001F and U+007F to U+009F) becomes a
   space. A notice is one line and never moves the cursor or sets colors.
2. A text of more than 500 Unicode code points keeps its first 499 and ends
   with `…` (U+2026). Count code points, not UTF-16 units (JavaScript
   `length`) or bytes (Go `len`). A cut may split a grapheme cluster.
3. A notice is hidden when one with the same `level` and the same shown text
   was shown less than 10 s earlier. A hidden notice does not restart that
   window.

The launcher should also cut texts at 500 code points before sending them.

## Redaction

- Neither side logs channel lines or the launch document verbatim.
- `dexcli` writes no log unless `DEX_AGENTS_TUI_DEBUG=1`. Its debug log may
  hold message types, methods, IDs, and error codes. It never holds an auth
  variable's value or `dex.auth` fields other than `mode`, and it writes the
  home directory as `~`.
- `display`, notice texts, and paths are shown on screen only.
- A gRPC error shows its status, Dex sub-status, and message, never its
  metadata.
- Error messages on the channel never hold a secret or an environment value.
- The contract host writes only environment names and message summaries
  (see [The contract host](#the-contract-host)).
- Fixtures use placeholder paths under `/home/user`. The tests reject any other
  home directory, this machine's home directory, and strings shaped like JWTs,
  provider keys, cloud keys, or bearer tokens.

## Conformance

[`../conformance/terminal_ui/`](../conformance/terminal_ui) holds hand-written
cases. The launchers (TypeScript and Python CLI cores) and the terminal UI
(Go) each run the cases that apply to them:

| File | Cases |
| --- | --- |
| `launch.json` | Launch lines and the check order: accepted, or the rejection code and exit code. Each schema case has exactly one violation. An implementation that uses a schema validator passes when the expected instance location and keyword are among its validator's errors; one that checks the rules in code passes on the code and exit code. |
| `describe.json` | `--describe` documents, valid and invalid, and contract choices. |
| `messages.json` | Valid and invalid channel messages, and method results. |
| `session_ids.json` | Key to session ID vectors, and keys that must be rejected. |
| `open_url.json` | URLs `open_url` opens or refuses. |
| `notices.json` | Shown notice texts, and which repeats are hidden. |
| `environment.json` | The environment a launcher gives `dexcli`, for each `dex.auth`. |
| `idempotency_table.json` | What the launcher side remembers of keys: retention and capacity. |
| `transcripts/*.json` | Whole channel exchanges: handoff, each method, cancel and recovery, `busy`, notices and Worker states, rejections, the ready timeout, and the other modes. |

### Transcripts

A transcript lists the messages in the order they cross the channel, per
direction:

```json
{"name":"chat_put_image","description":"…","real_roles":["launcher","tui"],"terminal_ui_environment":{},
 "steps":[{"from":"launcher","message":{"type":"launch",…},"ignore":["display",…]},
          {"from":"tui","message":{"type":"ready"}},
          {"from":"tui","message":{"type":"request","id":1,…},"ignore":["idempotency_key","params.path"]},
          {"from":"launcher","message":{"type":"response","id":1,"result":{…}}}],
 "tui_exit":0}
```

| Step | Meaning |
| --- | --- |
| `{"from", "message", "ignore"?}` | One message. `ignore` lists dotted paths that the receiving side does not compare. |
| `{"from", "message", "malformed": true}` | A message that fails its definition on purpose; see [Framing](#framing). |
| `{"from", "raw_line", "pad_to_bytes"?}` | Raw text, padded with spaces to `pad_to_bytes`. Over 1 MiB, the receiver must see a too-long line. |
| `{"from", "silence_ms"}` | `from` sends nothing for that long. |
| `{"close": role}` | That side closes the channel; the other reads EOF. |
| `{"pause_ms", "role"}` | That side waits before its next step. |

`tui_exit` is `dexcli`'s exit code, or `"terminated"` when the launcher stops it
with SIGTERM. `terminal_ui_environment` is the environment `dexcli` sees, for
the launch checks. `later_check_failure`, when present, declares a launch
that passes the line checks but fails a check after them: its `code`
(`terminal_failed` or `dex_setup_failed`) and its `cause`, such as a token
file that cannot be read. Without it, a launch that passes the line checks
must reach `ready`. `real_roles` names the sides a real implementation can
play in that transcript, deterministically, against a fresh Dex server:

- `tui`: a real `dexcli` reproduces it against the host as launcher.
- `launcher`: a real launcher reproduces it against the host as terminal UI.
  `launcher_setup`, when present, says what the test must arrange first,
  such as the files to alias or the Worker host to kill.
- An empty list: only the two host roles replay it, because no real side
  can force it. For example, `chat_unknown_method_and_busy` needs 4 requests
  to stay in flight, and a conforming `dexcli` never calls a method the
  launch document leaves out.

Transcripts that need server state a fresh server lacks (`id_conflict`, the
12-digit fallback), a race (cancel before an answer, a query that times
out), or a launch document no real launcher sends (one without
`get_request_result`) list only `tui`.

Values that differ between machines are matched like this:

- An ignored string binds the transcript's value to the value actually
  received, and later occurrences of the transcript's value stand for the
  real one. Ignoring an `idempotency_key` also binds both session IDs derived
  from it. So a transcript can expect a real `dexcli`'s random keys, or a
  real launcher's session ID and `web_url`, without fixing them.
- `--alias FIXTURE=ACTUAL` binds a value before the replay starts, such as a
  transcript path to a file the test made.
- Neither a binding nor an alias ever applies to a protocol constant: the
  value of a `type`, `method`, `methods`, `code` (also `error.code`), `mode`,
  `state`, `level`, `contract`, or `language` field, at any depth. They
  always compare as written.
- A bound value of 8 or more characters is also replaced inside longer
  strings, longest first and in one pass, so a URL under `web_url` or a path
  that holds a session ID follows the binding.
- Free text is never compared: an error's `message` and `launch_rejected`'s
  `message`.
- Before comparing, the host checks every message it receives against the
  schemas, whole, ignored paths included: the launch document against
  `terminal-ui-launch.schema.json`, other messages against their direction in
  `terminal-ui-channel.schema.json`, and a response's `result` against its
  method's `<method>_result`. A violation fails the replay. Only a step
  marked `malformed`, and a launch document the transcript rejects, skip
  this. The host checks with its own standard-library validator of the
  schema keywords these files use, and the tests check that it agrees with
  `jsonschema` on every case.
- Launch steps ignore what depends on the command and the machine:
  `command`, `dex.server_address`, `dex.web_url`, the session's `flow_type`
  and `session_id`, `display`, `capabilities`, `log_path`, and `connectors`.
  Responses ignore the stored image's `uri`, `sha256`, and `size_bytes`, and
  the connector rows.

### What the conformance tests prove

`tests/test_terminal_ui_contract.py` proves that the fixtures, the schemas,
and the Python reference functions in the contract host agree, and that each
transcript is a sequence this document allows: request ID order, one
response per request, keys, `busy`, `unknown_method`, method results and
errors, derived session IDs, `open_url` decisions, and the launch checks. It
also runs the two roles of the contract host against each other.

It does not prove that any real implementation behaves this way. It never
calls `StartFlow` or `DescribeFlow`, keeps no real key table, restarts no
Worker host, loses no real response, and runs no TypeScript or Go code.
Each pull request that implements a side of this contract must add, as a
condition of merging:

- tests that run that side's own code over every case file above, including
  the session ID vectors, `open_url`, notices, and the key table;
- integration tests of the real processes: the terminal UI against the
  contract host as launcher, and each launcher against the contract host as
  terminal UI and with its real Worker host and a Dex server;
- fault injection: a lost response, cancel racing completion, a forgotten
  key, a Worker host killed in the middle of a request, a `start_session`
  resent after the Flow started, and a 6-digit ID collision.

### The contract host

[`tests/terminal_ui_contract_host.py`](tests/terminal_ui_contract_host.py)
plays either side of a transcript and checks the other. It needs Python 3.11
or later and only the standard library, so Go, TypeScript, and Python tests
can all run it.

Play the launcher against a terminal UI, on a pseudo-terminal, to test
`dexcli agents-tui`:

```bash
python3 agents/cli/tests/terminal_ui_contract_host.py launcher \
  --transcript agents/conformance/terminal_ui/transcripts/chat_put_image.json \
  --pty 80x24 --screen-log /tmp/screen.log --record /tmp/launcher.jsonl \
  -- /path/to/dexcli agents-tui
```

It passes the channel on an fd of 10 or higher, gives the terminal UI only
the allow-listed environment (`--extra-env NAME=VALUE` adds more for a
test), replays the launcher side, and exits 0 when everything matched and
the exit code is `tui_exit`, or 1 with the first difference on stderr. With
`--pty-input-from-stdin` it copies its own stdin to the PTY, so a test can
type the keys that make the terminal UI send its requests.

Play the terminal UI against a launcher, to test a CLI core: point `DEXCLI` at
a wrapper. The launcher gives `dexcli` no `PATH`, so write the interpreter's
absolute path:

```sh
#!/bin/sh
exec /absolute/path/to/python3 /absolute/path/to/agents/cli/tests/terminal_ui_contract_host.py tui \
  --transcript /absolute/path/to/chat_ready_then_exit.json --record /tmp/tui.jsonl "$@"
```

It answers `--describe` (or prints `--describe-file`), replays the terminal UI
side over `--channel-fd`, and exits with `tui_exit`, or 70 on a difference.
Both roles take `--alias FIXTURE=ACTUAL`, for example
`--alias /home/user/pictures/screenshot.png=/tmp/case/screenshot.png`, to
stand a test's file or value in for the transcript's.
Its record starts with the arguments, the environment's names (never their
values), and the terminal size and foreground state. It then lists a summary
of every message it sent and received: type, ID, method, contract, mode,
state, level, and error code. It never writes a message's texts, paths, or
other values, in its record or on stderr; a type, method, or code that is not
a short lowercase name shows as `(other)`. A difference names only the fields
this contract defines and counts the others, and never shows values.

Run the checks:

```bash
agents/cli/tests/ci.sh
```

## Worker host lifetime

This section is not part of the channel contract: the terminal UI never sees
it. It fixes what both CLI cores do, so that their Worker hosts end the same
way.

### Bounds after the launcher dies

The watchdog runs outside the Worker host's event loop: a worker thread in
Node, a daemon thread in Python.

1. Every 1 s it checks whether the parent is still the launcher. When it is
   not, it starts an orderly shutdown and arms the hard bound at once.
2. A shutdown that starts for another reason (EOF on the control channel,
   `shutdown` from the launcher, SIGTERM) also arms the bound at once.
3. Python: 15 s after the bound is armed, if the Worker host still runs, the
   watchdog sends SIGTERM to every registered tool process group, waits up to
   2 s for them, and sends SIGKILL to those left.
4. It then sends SIGKILL to the Worker host's own process group. In Node this
   is all the watchdog does at the bound: it never signals a tool process
   group, because a worker thread cannot tell whether the main thread already
   reaped the group's leader. The main thread stops the tool groups during an
   orderly shutdown, and signals a group only while its leader's
   `ChildProcess` has not reported its exit.

Python's watchdog may signal tool groups (step 3) because the Python shell
tool owns every reap of a tool group leader: it spawns the shell with
`subprocess.Popen` outside asyncio's child watcher, waits for its exit
without reaping it (`waitid` with `WNOWAIT`, or kqueue on macOS before
Python 3.13), stops what the shell left in its group, and only then reaps it
with `waitpid`, under the registry lock that every signal also takes. A
registration whose run record cannot be written is undone, and that shell is
killed and reaped at once. So, within a running Worker host, no signal
reaches a reaped leader's reused pid. A core whose tool leaders are reaped
elsewhere, such as by an event loop's child watcher, must not signal tool
groups from its watchdog.

In Node, the leader of a tool group is a gatekeeper shell: it runs the
command with `/bin/sh -c`, reports the command's exit status to the main
thread, and waits until the main thread has stopped what the command left in
the group (SIGTERM, then SIGKILL 0.5 s later). So the leader stays unreaped,
and its pid stays the group's, for as long as the main thread may signal the
group. When the Worker host goes away first, the gatekeeper kills its own
group once the command ends.

Each number below adds scheduling delay, and holds only while the watchdog
can run:

| Platform and core | Launcher died | A shutdown began |
| --- | --- | --- |
| Node (TypeScript), macOS and Linux | Worker host gone within about 1 + 15 = 16 s. Registered tool groups are gone within that bound when the orderly shutdown runs; when the main thread is stuck they are **not** bounded, and the next start's leftover cleanup stops them | about 15 s, with the same rule for tool groups |
| Python, macOS | Worker host and registered tool groups gone within about 1 + 15 + 2 = 18 s | about 15 + 2 = 17 s |
| Python, Linux | The Worker host dies at once: Python sets `PR_SET_PDEATHSIG` to SIGKILL, so the kernel kills it with the launcher, and its watchdog dies with it. Registered tool groups are **not** bounded: they run until the next start's leftover cleanup stops them | about 15 + 2 = 17 s |

The bounds do not hold:

- while the Worker host is stopped (SIGSTOP) or stuck in the kernel;
- in Python, while the main thread holds the GIL in native code;
- for tool groups on Python and Linux after the launcher died, and in Node
  while the Worker host's main thread is stuck, as above;
- for processes a harness starts outside the Worker host's group and never
  registers, such as the tool processes of an agent framework's own CLI.

### Process identity for leftover cleanup

The Worker host writes a run record of itself and of each tool process group
it starts. On a later start, a launcher stops what a dead launcher left, and
checks each group's identity again right before that group's own SIGTERM and
before its SIGKILL, because signaling one group can end another group's
leader.
Any mismatch, or a process that is gone, removes the record entry and sends
nothing.

| Record | Identity that must still match |
| --- | --- |
| Worker host | pid, process group ID, being that group's leader (pid equals pgid), start time, and the SHA-256 of its command line |
| Tool process group leader | pid, process group ID, being that group's leader, and start time |

A tool group leader is a shell that may `exec` its command, which changes its
command line but not its pid, group, or start time. So the command line is
not part of a tool group's identity. The start time is what the system
reports, such as `ps -o lstart` in the C locale. A record holds the SHA-256
of the Worker host's command line (UTF-8, as `ps -ww -o command=` prints it),
never its text, because a command line can carry a secret.

This cleanup is best effort, not a proof of ownership. The start time has
one-second resolution, and the identity check and the signal are separate
steps. A pid reused within the same second, or between the check and the
signal, could still be signaled. Checking right before each signal keeps that
window small; nothing removes it.
