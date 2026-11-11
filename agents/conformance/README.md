# Dex Agents conformance suite

Every Dex Agents core (Python, TypeScript, and the Go, Java, and Rust cores
that follow) runs the same session Flow contract. A session started by one
language can run on Workers of another, and a UI reads any of them. This
suite fixes every value that crosses that boundary, so each core can prove it
speaks the same contract. It is decision 1 of
[agents-go-java-rust.md](../../docs/design/plan/agents-go-java-rust.md), and
the contract of record is
[agent-framework-adapters.md](../../docs/design/plan/agent-framework-adapters.md)
with the M2 additions in
[agents-m2-stepwise.md](../../docs/design/plan/agents-m2-stepwise.md).

The files are generated from the Python core and checked against the
TypeScript core, except `connector_tools.json`, `mcp_dex_conventions.json`,
and `images.json`, which are hand-written and read by both cores' connector
tool, MCP tool, and image tests, and `cli_config.json` and
`cli_session_flow_types.json`, which are hand-written for the CLI runtimes in
[`../cli`](../cli). A core passes when it passes all four parts below.

| Path | What it fixes |
| --- | --- |
| [`values/`](values) | Golden wire values, one file per value type |
| [`names.json`](names.json) | Step types, RPCs, persistence names, event types, enums, map keys, ID and timestamp formats |
| [`texts.json`](texts.json) | Every model-visible text the session writes |
| [`transcripts/`](transcripts) | Scripted-turn scenarios and the transcript each core must reproduce |
| [`session_graph.json`](session_graph.json) | The session Flow Definition Graph that Dex Web loads, with placeholders for the Flow type and the Action permission |
| [`session_graph_renderings.json`](session_graph_renderings.json) | The hash of the graph text each core must render |
| [`connector_tools.json`](connector_tools.json) | Connector tools: argument checks against `inputSchema`, refused schemas, branch mapping (with `mayHaveDispatched`, the fence check, when a case sets it), fence observation (the saved markers and earlier checkpoint that decide `mayHaveDispatched` and proof of no send; `<callId>` and `<otherCallId>` stand for the call's Call ID and another call's), default tool names (operation ID to snake case, as connectorctl `lowerCamelToSnake` names Python Step factories), result text, and truncation. Hand-written, not generated |
| [`mcp_dex_conventions.json`](mcp_dex_conventions.json) | Dex MCP conventions: the `_meta` keys, how a tool definition's keys pick its effect, approval, and order, and how a result's or a JSON-RPC error's outcome key is recorded, with the exact text. Hand-written, not generated |
| [`cli_config.json`](cli_config.json) | CLI configuration: YAML parsing, schema errors, URL checks, file defaults, session settings narrowing, operation names, and the shell allow list. Hand-written for the CLI runtimes; see [`../cli/README.md`](../cli/README.md) |
| [`cli_session_flow_types.json`](cli_session_flow_types.json) | CLI session Flow types: the 6-character base36 suffix each CLI runtime derives from the host name, user, configuration identity, and workspace, with ASCII, Windows, Unicode, and left-padded cases. Hand-written for the CLI runtimes |
| [`images.json`](images.json) | Image references: structural byte checks and the size limit, type checks on read, reference validation and its error texts (URI length in UTF-8 bytes), list limits, how S3, local directory, and in-memory stores name objects, and the local directory store's root rules and error texts. Hand-written, not generated |
| [`generate.py`](generate.py) | Writes all of the above from the Python core |
| [`session_graph.py`](session_graph.py) | Derives the session graph from the Python core's registered Flow and source |

## Comparison rule: canonical JSON for stored values, exact bytes for model-visible text

The cores write different bytes for the same stored value, and all of them
decode each other. The Python Dex SDK sorts keys and escapes non-ASCII. The
TypeScript SDK keeps field order and writes raw UTF-8. Go's `encoding/json`
keeps field order and escapes `<`, `>`, and `&`. The contract fixes JSON
field names and values, not key order or escaping, so the suite does not
require one stored byte form.

**Stored values compare in canonical JSON.** Parse the core's encoding, then
serialize it with object keys sorted by code point, no whitespace, and every
character outside ASCII escaped as lowercase `\uXXXX` (surrogate pairs above
U+FFFF). `"`, `\`, and control characters use JSON's short escapes (`\n`,
`\t`, `\b`, `\f`, `\r`) or `\u00XX`. `<`, `>`, `&`, and `/` are not
escaped. Integers have no fraction or exponent. This is the Python Dex SDK's
output, so Python's own encoding is already canonical. This applies to every
Attribute, AttributeMap, Channel, ChannelMap, Stream, Step input, and RPC
value.

**`AgentEvent.payload` compares in canonical JSON too.** It is JSON text
inside a string, and the contract only says "JSON object text". The Python
core writes sorted keys with `", "` and `": "` separators, the TypeScript core
writes sorted keys compactly, and Go matches Python. A UI parses the payload,
so the suite parses it and serializes it canonically before comparing. The
`wire` of each `AgentEvent` case keeps the Python writer's bytes, and its
`canonical` holds the canonical payload.

**The Stepwise tool source envelope compares in canonical JSON too.** A call
that a Stepwise tool source built stores `AgentToolCall.tool_source_reference`
as `{"stepwise_tool_source": "<source name>", "reference": <the source's own
reference or null>}`. The model never sees it. Python writes it with
`json.dumps` defaults (spaced, non-ASCII escaped), and TypeScript writes it
compactly with raw UTF-8. `names.json` lists both written forms: every core
must read each one, and its own form must canonicalize to `canonical`. The
inner `reference` string is the source's own text and stays byte for byte.

**Model-visible text compares byte for byte.** A model sees these strings
exactly as written, so a different byte is a different prompt:

- every entry in `texts.json`: notes, tool results, rejection and
  cancellation JSON, plan texts, summary texts, built-in tool definitions,
  and argument errors;
- in the transcripts, every `model_requests` item, and stored message fields
  the model reads back: `framework_item`, `tool_arguments`, `tool_output`,
  and `text`;
- string fields inside stored values, such as `AgentToolCall.arguments` and
  `output`. Decoding and re-encoding must leave them unchanged.

Some model-visible JSON text is fixed by the contract or by both cores: the
cancellation results and `ask_user`, `durable_wait`, and `write_todos`
outputs are compact with non-ASCII unescaped, and function tool outputs and
scripted model arguments use Python `json.dumps` spacing (`", "`, `": "`) with
non-ASCII unescaped. MCP JSON that a model sees (resource and prompt tool
outputs, non-text content blocks, and structured content of tool results) is
written with keys sorted by code point, `json.dumps` spacing, non-ASCII
unescaped, and whole floats as integers. Sort while writing the text: a
JavaScript object puts integer-like keys first. Match those bytes; do not
canonicalize them.

## Golden values

Each file in `values/` is one value type:

```json
{
  "type": "AgentToolCall",
  "wire_kind": "json",
  "description": "One AgentToolCalls map instance.",
  "cases": [{ "name": "full", "description": "...", "wire": "...", "canonical": "..." }]
}
```

- `type` is the language-neutral type name that `names.json` uses. Scalar
  types are `string`, `int64`, `bool`, and `null`.
- `wire_kind` is the Dex Value kind: `json`, `string`, `int64`, or `bool`.
- `wire` is the Value's data as text: the JSON text for `json`, the string
  itself for `string`, a decimal for `int64`, and `true` or `false` for
  `bool`. Decode it.
- `canonical` is what the core's encoding of the decoded value must equal
  after canonicalization. For values the Python core writes, `wire` equals
  `canonical`, except that an `AgentEvent`'s `wire` payload keeps the Python
  writer's spacing. For values another writer stored, such as
  `released-0.1.0` shapes from dex-agents 0.1.0, explicit nulls, and fields
  added later, `canonical` shows the defaults filled in.

Case names say what they cover: `full` sets every optional field, `defaults`
leaves every optional field at its default, `released-0.1.0` is the shape
dex-agents 0.1.0 wrote, and `status-*`, `effect-*`, `kind-*`, and `type-*`
sweep an enum. The `AgentEvent` cases have one event per event type.

## Names manifest

`names.json` lists, for the session Flow of any Flow type:

- `step_types`: each Step type, in registration order, its input type, and
  whether it is the start Step;
- `rpcs`: each RPC name, its input and output types, whether it is
  transactional, the Attributes it locks, and the AttributeMaps and Channels
  it loads;
- `persistence`: each Attribute, AttributeMap, Channel, ChannelMap, and Stream
  name with its kind and value type, plus the default `AgentEvents` capacity;
- `event_types` and `enums`: every value of each closed set;
- `keys`, `ids`, and `timestamps`: map instance keys, turn and interrupt ID
  formats, and the RFC 3339 format with examples;
- `limits`: snapshot and page limits, the default list page size, the default
  durable wait and Stepwise model call limits, the channel read limit,
  `max_session_settings_bytes`, `session_settings_size_tolerance_bytes`, and
  the default message window size, chunk size, chunk byte cap, chunks moved per
  commit, and retention limit, each taken from the Python core's constants;
- `session_settings_sizes`: settings with the size every core's
  `session_settings_size` must compute (UTF-8 bytes of the core's own compact
  JSON text, non-ASCII unescaped). `bytes` is every core's size unless
  `bytes_by_core` gives one per core: Python writes floats in shortest form
  (`1.0`, `1e+16`) where TypeScript and Go write `1` and every digit, and Go
  escapes U+2028 and U+2029. The `limit` cases stand for
  `{"note": "x" * note_length}`, so the file stays small; `is_accepted` is the
  Client check at `max_session_settings_bytes`, and `is_accepted_by_session`
  the session's check with `session_settings_size_tolerance_bytes` more;
- `final_tool_call_statuses`: the statuses a record keeps;
- `mcp_resource_and_prompt_tool_names`: the read tools an MCP tool source
  offers for resources and prompts;
- `stepwise_tool_source_reference`: the tool source envelope above.

`tool_call_progress` is in the M2 plan's event table, but no core writes it
yet, so the manifest leaves it out until one does.

## Text table

`texts.json` holds `constants`, `templates` with inputs and the exact text
each renders, the built-in tool definitions with their compact JSON Schema
text, and the error each built-in tool's argument validation reports. It
also has the unknown-tool error (`unknown tool <name>`, which the model sees
after `The tool call failed: `), the MCP resource and prompt tools'
definitions and argument errors, the MCP tool output serialization (sorted
keys, `json.dumps` spacing, non-ASCII unescaped, and whole floats written as
integers), and the text of MCP tool results with structured content,
non-ASCII text, and a non-text block. Record
inputs are wire JSON; decode them with the core's codec first.
`language_specific` texts name a method in the core's own language, so they
are listed, not compared.

## Session Flow Definition Graph

`dexcli visualize` cannot read the session Flow, so `session_graph.py` derives
the graph. The registered Flow gives Steps, RPCs, persistence names, locks, and
failure routes. The source of `session_flow.py` gives wait conditions,
transitions, and Channel publishes. Step explanations are the Step docstrings.
Only the group names and the Action words are written by hand. The graph leaves
out Attribute reads and writes.

`generate.py` writes the same file here and into the Python and TypeScript
packages. Never edit a copy: change the session Flow, run `generate.py`, and
commit the result. These tests fail otherwise:

- Python: `test_the_suite_files_match_the_python_core` fails on any stale copy.
  A change to a Step, RPC, Channel, lock, failure route, wait condition, or
  transition changes the derived graph.
- TypeScript: `session-flow-graph.integration.test.ts` compares the graph with
  the registered Flow and with the TypeScript source, which it scans for wait
  conditions, routes, and publishes.
- Both cores render `session_graph_renderings.json` byte for byte.

The Go core registers fewer Steps and RPCs than the graph shows, so it ships no
graph yet.

## Scripted-turn transcripts

Each file in `transcripts/` is one scenario: a read, an approval, a question,
a durable wait, a steer, plan execution, compaction, a tool task, and a call
to an unknown tool. Every
core builds the same Stepwise agent, plays the `actions` against a private
Dex engine, and compares the result with `expected`.

The agent, as built in
[`conformance_agent.py`](../python/core/tests/conformance_agent.py) and
[`conformance-agent.ts`](../typescript/core/test/fixtures/conformance-agent.ts):

- Two Flow types, `ConformanceAgent` (profile `chat`) and
  `ConformanceCompactingAgent` (profile `compacting`, with
  `context_window_tokens` 1000, `trigger_ratio` 0.3, `retained_ratio` 0.15).
  Both enable every built-in tool and plan mode, and use the instructions
  `You are the conformance agent.`
- Tools: `lookup_order(query)`, a `read` that returns `order <query>:
  shipped`; `send_email(to, body)`, a `non_idempotent_write` that returns
  `{"sent_to": to, "body": body}` as Python `json.dumps` text, in exactly that
  key order (Go's `encoding/json` sorts map keys, so return an ordered struct,
  not a map); and
  `start_report(topic)`, a `read` whose call returns a tool task
  `task-<tool_call_id>` with a 1-second interval, finished by the first poll
  as `report on <topic>`.
- The model is the core's scripted model client. It answers from the
  scenarios' `replies`, keyed by the request's last user text. The reply
  index advances past each reply's tool call count as tool results arrive.
  Tool call IDs keep the default `<step_execution_id>-<index>`.

Actions are `start_session`, `send_user_message`, `resolve_interrupt`,
`answer_question`, `steer_user_message`, `execute_plan`, `snapshot` (stores
the `get_agent_snapshot` output under a label), `wait_for_idle`, and
`wait_for_interrupt`. An action with `returns` must return that value.

`expected` holds:

- `steps.executed`: every Execute completion in history order, as Step type,
  Step execution ID, and final attempt. `steps.waits_completed` is the sorted
  set of WaitFor completions, because parallel Steps finish their WaitFor in
  engine order.
- `events`: every `AgentEvents` message in order, with its payload in
  canonical JSON.
- `snapshots`: the labeled snapshots and `final`, as wire JSON. Tool call
  records are deleted when a turn ends, so their final state shows in the
  stored tool messages, the `tool_call_finished` events, and the snapshots
  taken while a call waits.
- `model_requests`: every model call's instructions, offered tools (name,
  description, and compact JSON Schema text, as sent), tool choice, and input
  items, in order. The fixture's function tool schemas are written out
  explicitly, with `additionalProperties: false`, so every core offers the
  same text.

Every RFC 3339 millisecond UTC timestamp is replaced with `<timestamp>`. A
timestamp in another format stays and fails the comparison.

MCP servers are not part of the transcripts; each core's own MCP tests cover
them, and the suite fixes the MCP texts above. The transcripts need a Dex
engine. Python runs all nine in about 12 seconds
and TypeScript in about 10, so they run on every PR in each core's `ci.sh`.
Each scenario fails after 90 seconds rather than waiting out every poll.

## Regenerate

```bash
cd agents/python/core
uv run --frozen python ../../conformance/generate.py               # values, names, texts
# dex-server-zd must sit next to dexcli, or set DEX_SERVER_ZD_BINARY.
DEXCLI=/path/to/dexcli uv run --frozen python ../../conformance/generate.py --transcripts
uv run --frozen python ../../conformance/generate.py --check       # values, names, texts only
```

`--check` covers `values/`, `names.json`, and `texts.json`. It does not run
the transcripts, which need an engine; the tests check those. In CI, the
Python core's `tests/test_conformance.py` fails when a file is stale, and
`tests/test_conformance_transcripts.py` fails when a transcript differs.
Regenerate only for an intended contract change, and run the TypeScript core
against the result before you commit it.

## CI

`Agents CI` runs a job for every `agents/<language>/<package>/ci.sh`. The
Python core's `pytest` runs `tests/test_conformance.py` and
`tests/test_conformance_transcripts.py`; the TypeScript core's
`test:integration` runs `test/integ/conformance-*.integration.test.ts`. A
change under `agents/conformance/` matches the workflow's `agents/**` path
filter, so every core runs it. `agents/cli/tests/ci.sh` checks
`cli_config.json` against the CLI schemas and a test-only reference resolver.

## Adding a core

A new core, such as Go at `agents/go/core`, passes the suite like this:

1. Read the suite relative to the package: `../../conformance/`.
2. Implement canonical JSON serialization as the comparison rule above
   defines it. In Go, re-serialize from a generic `any` with sorted keys and
   those escapes; the core's own stored bytes may stay as `encoding/json`
   writes them.
3. Map each `type` name to the core's codec. For every case in `values/`,
   decode `wire` and `canonical`, re-encode, then parse the result,
   canonicalize it (and an `AgentEvent`'s payload), and compare with
   `canonical`. Go through the registered definitions where you can: each
   `persistence` entry's codec, each Step's input codec, and each RPC's input
   and output codecs, so a wrong codec on a name fails.
4. Compare `names.json` with the core's registered session Flow and
   constants: Step types in order, RPC names, transactional flags, locks and
   loads, persistence names and kinds, event types, enum values, final
   statuses, key examples, interrupt ID formats, and every `limits` entry
   against the core's own constants. Compute each `session_settings_sizes`
   size with the core's own function, using the core's own entry in
   `bytes_by_core` when there is one, and check that the core's Client and
   session checks accept and refuse the limit cases as `is_accepted` and
   `is_accepted_by_session` say.
5. Render every `texts.json` constant and template with the core's own
   functions and compare byte for byte, with no canonicalization. Do the same
   for the MCP resource and prompt tool definitions and argument errors.
   Read every written form of the tool source envelope, and check that the
   core's own form canonicalizes to `canonical`. Validate each built-in tool argument
   case and compare the error text.
6. Build the conformance agent above with the core's Stepwise runner and
   scripted model client. Keep `send_email`'s output keys in the order
   `sent_to`, `body`; in Go, return an ordered struct, not a map. Then play
   every scenario in `transcripts/` on a private engine, normalize as above
   (timestamps, the sorted WaitFor set, and canonical event payloads; nothing
   else), and compare with `expected`.
7. Add the tests to the core's `ci.sh`. Any mismatch is a bug: fix the core
   that diverges from the contract doc, and regenerate only when the contract
   itself changes.
