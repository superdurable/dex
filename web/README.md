# Dex Web

The live Flow overview lists pending Channel messages with their server-assigned
IDs and decoded values. Operators can delete a pending message. Temporal performs
the deletion atomically; the Cadence query-plus-signal fallback is best effort and
can race message consumption.

Flow Definition Graphs show resource-read edges for AttributeMap and Channel
message snapshots explicitly selected by Python RPC decorators. Channel size
metadata does not add a resource-read edge because every RPC receives it.

Dex Web searches flows and displays Dex semantic history, step topology,
live flow state, time travel points, and stop controls.

The production Web server is Go. It serves an embedded React SPA and translates
same-origin HTTP/JSON requests under `/api/` to Dex `FlowService` gRPC calls.
Temporal/Cadence credentials and backend history never enter the browser.

`GetHistoryEvents` returns the same `input`, `output`, and `context` step-event
shape for sync, async, and async-fallback execution. Timeline, Execution graph,
Time Travel and Selected event use that common structure.

`POST /api/blobs/load` batches Dex `LoadBlobs` calls. The browser recursively
hydrates the selected event and current flow state, dedupes by blob kind and ID,
and caches loaded values across tabs. Missing values are labeled
`Value blob unavailable`. Terminal async failures without a retained invocation
snapshot explain the short-retry behavior in Selected event without raising a
page-level data warning. Neither state exposes blob IDs or storage paths.

## Run through dexcli

```bash
dexcli dev
```

Open [http://127.0.0.1:8802](http://127.0.0.1:8802). No Node.js process runs in
this mode.

To load Flow Definition Graph JSON files into the independent **Flow Rendering**
page, provide a directory at startup:

```bash
dexcli dev --flow-rendering-dir ./build/flow-definitions
```

Without an `active-manifest`, Dex recursively scans JSON files on every
definition-dependent request. Generate them with `dexcli visualize SOURCE
--json --out ./build/flow-definitions/name`; changes become visible without a
Dex Web restart. Invalid JSON, unsupported schema versions, and source read
failures return a typed 503 and make `/readyz` fail.

Starting Dex with `--flow-rendering-dir` opens **v2** at `/v2`. The top-right
Version menu returns to **v1**. Without that directory, Dex Web opens **v1** at
`/v1/flows`. v2 lists current runs for one Flow type on the left and renders
that type's Flow Definition Graph on the right. Run lists omit executions
closed by continue-as-new. Summary, Display, edits, and
Actions target the current run without accepting a Run ID. Version 1 and
Version 2 files can coexist. Each definition's Flow type is the one Workers
register, so Go Flows need no `GetFlowType` or `GetStepType` override unless
`dexcli visualize` reports a Flow or Step name diagnostic.
Duplicate valid Version 2 definitions for one Flow type, such as two
package-main Flows named `main.Flow`, make the definition source invalid, and
the error names both files. Invalid analyzer output remains visible on **v1** Flow
Rendering but does not appear as a **v2** Flow type.

### Start Flow

The v2 Run workspace shows **Start Flow** when the selected definition contains
a `v2.start` schema and `web.workQueuePermissionMode` is `local-selector`. The
dialog asks for a Flow ID, a plaintext Worker gRPC address, and the generated
start input. It recursively renders objects, arrays, string-key maps, enums,
booleans, datetime values, optional fields, and nullable fields. Integer input
stays as decimal text until the request is serialized, so int64 values do not
pass through JavaScript floating-point numbers.

Dex Web checks the Worker `host:port` from the BFF when the address loses focus
and again before starting. An unreachable port shows a warning and blocks the
start until the operator explicitly selects **Bypass worker health check**.
The probe opens a TCP connection and does not invoke a Worker method.

`POST /api/v2/start` accepts `flowType`, `flowId`, `workerTargetAddress`, and the
raw JSON `input`. It also accepts `bypassWorkerHealthCheck`; false performs the
server-side port check and returns `WORKER_UNHEALTHY` with HTTP 412 when the
target is unreachable. `POST /api/v2/worker-health` checks the same address for
the dialog. Start requires `X-Dex-Flow-Definition-Revision`, reloads the current
definition snapshot, validates every input field again, and rejects unknown
fields. The Server chooses the start Step type and generates the request ID.
The browser cannot set Worker headless routing.

Configure headless routing once for every Flow started from this Dex Web
instance:

```yaml
web:
  startFlowWorkerTargetHeadless: false
```

The equivalent environment variable is
`DEX_WEB_START_FLOW_WORKER_TARGET_HEADLESS`. The default is false and the value
is immutable after startup. Set it to true when the entered Worker addresses
are Kubernetes headless Service targets. Start Flow is unavailable in
`trusted-header` mode.

The v2 Work Queue is available at `/v2/work-queue`. In the default
`local-selector` mode, its **Working as** control
selects one Action permission and filters on the Server-maintained
`DexWorkQueuePermissions` Search Attribute. The Worker submits the complete
Action permission mapping only when an invocation writes an Action condition
source. Dex Web submits the same mapping only when `SetAttributes` edits one of
those sources. The Server overlays the writes on authoritative Attribute state
and atomically adds newly matching permissions to the projection. Once added,
a permission remains searchable for that Flow execution. `POST
/api/v2/search` also accepts several `workQueuePermissions`; they are matched
with OR, then combined with the Flow type and other filters using AND. This
selector is a local development boundary. In hosted deployments,
`trusted-header` hides the selector, ignores request-body permissions, and
authorizes Search and Actions only from
`X-Dex-Work-Queue-Permissions`. A trusted reverse proxy must strip any
client-supplied value before injecting its own. Port 8802 must not be reachable
around that proxy.
Dex does not expose an `/api/v2/access` endpoint; hosted identity and role
resolution stay in the reverse proxy and hosting control plane.

The local v2 Connections page is available at `/v2/connections` when Dex Web
runs through loopback-bound `dexcli dev` with a local Flow Definition source.
It groups Connector Steps by Connector ID and static connection name, resolves
the exact official Connector release declared by the graph, verifies release
and Studio UI checksums, and loads UI bundles in opaque-origin sandbox iframes.
Without a Studio bundle, it renders the manifest fields directly.

During Connector development, the repeatable
`--connector-release-override connector-id=artifact-directory` flag on
`dexcli dev` can replace release resolution for selected Connector IDs. This
allows Flow graphs produced from `go.work` modules to use the local release
metadata and Studio UI. Connections labels these sessions as **Local
override**. Overrides are accepted only by loopback local Connector setup and
retain the normal metadata, checksum, Host API, and safe-archive validation.

The setup tabs expand vertically below the selected named connection. They put
authorization first and disable every Flow configuration until the connection
is ready. Each Connector Step or Trigger binding that declares
`ConfigurationUI` gets one tab. Dex Web mounts only the selected tab's
sandboxed unit iframes and marks the tab configured after its scoped record has
been saved. The Connector bundle renders only the small pickers and inputs and
reports bounded content heights through a nonce-bound `connector.frame.resize`
message. Dex Web validates the message source and resizes each iframe without
reading its opaque-origin DOM. Unit ports are merged at their declared JSON
Pointers, so Flow code controls composition without coupling a Connector's
connection screen to one example. The host protocol for this contract is 0.2.

The `connector.host.ready` message also carries three optional fields. `theme` is
the theme the frame paints, `light` or `dark`. The Connections page has no dark
theme yet, so Dex Web always sends `light`. It also sets the iframe element's
`color-scheme` to the same theme, because a frame whose scheme differs from its
element gets an opaque canvas. `themeTokens` is a flat map from `--studio-*` CSS
custom property names to values. Dex Web reads each value from an allowlisted
v2 token in `app/v2/css/tokens.css`, resolved from the light ramps, so a Dex Web
restyle reaches every bundle without a Connector release.

Each value must match one grammar, which `sdk/react` in dex-connectors-library
applies too:

- Colors are `#` with 3, 4, 6, or 8 hex digits, `rgb(R, G, B)`, or
  `rgba(R, G, B, A)`, in comma syntax. Channels are integers from 0 to 255
  without leading zeros. Alpha is 0 to 1 with at most four decimals.
- Radii are `0` or a non-negative `px` length with at most three digits on
  each side of the decimal point.
- `--studio-focus-ring` is one box shadow: an optional `inset`, two to four
  `px` lengths that may be negative, and one color.
- `--studio-duration` is up to four digits of `ms`, or seconds with at most two
  integer digits and three decimals, such as `160ms` or `.16s`.
- A font stack has 1 to 16 comma-separated families. A family is a quoted name
  of letters, digits, spaces, and hyphens, or unquoted words that each start
  with a letter, optionally after one hyphen. The CSS-wide keywords, `default`,
  and `none` are accepted only when quoted.
- A value is at most 256 characters.

Dex Web drops any other value, and the bundle keeps its built-in value for that
property.

`stylesheet` is the whole Connector Studio stylesheet: layout, spacing,
component rules, and the default token values for `light` and `dark`. Its only
source is `app/v2/connections/connectorStudio.css`. Dex Web imports that file as
text with Vite's `?raw` suffix, removes its comments, and sends the result in
every ready message along with `theme` and `themeTokens`. A bundle built with
`sdk/react` replaces its compiled `connectorStudioStyles` with this text and
then applies `themeTokens` on top. A restyle of any Studio rule is therefore a
Dex Web change only. The compiled copy in `sdk/react` is the fallback for hosts
that omit the field, so a restyle does not need to change it. Only new markup
or new classes need a Connector release.

Bundles render only the `studio-*` class contract, which `sdk/react` exports as
`connectorStudioClassNames`: `studio-surface`, `studio-header`, `studio-muted`,
`studio-field`, `studio-actions`, `studio-button`, `studio-button-primary`,
`studio-notice`, `studio-notice-info`, `studio-notice-success`,
`studio-notice-error`, `studio-notice-attention`, `studio-options`,
`studio-option`, `studio-option-label`, `studio-option-id`,
`studio-option-detail`, `studio-badges`, `studio-badge`, and `studio-checkbox`.
The sent stylesheet must style every class in the contract and no other
`studio-*` class. It must also follow these rules:

- It is printable ASCII and at most 65,536 characters. It never contains `</`,
  so it cannot close the frame's `<style>` element. A bundle ignores a longer
  stylesheet or one containing `</`, and keeps its compiled one.
- It has a `.studio-<name>` selector for every class in the contract. A bundle
  also ignores a stylesheet that lacks a selector for any class in the
  `connectorStudioClassNames` it was built with. Renaming or dropping a class
  in `connectorStudio.css` therefore sends every released bundle back to its
  compiled stylesheet, and those bundles miss every later restyle until their
  connectors are released again. Keep every class, and add new ones only.
- It has no at-rules such as `@import` or `@media`, no comments, no backslash
  escapes, no `!important`, and no `url()` or `expression()`.
- A selector is a `:root` or `:root[data-theme='dark']` token block, `html`,
  `body`, or starts with a `studio-*` class. Element names, attribute
  selectors, and the `:hover`, `:focus-visible`, `:focus-within`, `:disabled`,
  `:checked`, and `:not()` pseudo-classes appear only after that class.
- Token blocks declare only `color-scheme` and the allowlisted `--studio-*`
  properties, and every value matches the `themeTokens` grammar. `:root`
  declares every allowlisted property.
- Every other declaration uses only numbers with `px`, `fr`, `%`, `ms`, or
  `s`, `var()` references to allowlisted properties without fallbacks, and the
  layout keywords the test lists. No other function is allowed, and the only
  color keyword on that list is `transparent`, so every other color comes
  from a token. Named colors such as `red` and `currentcolor` are rejected.

`themeTokens` are inline properties on the frame's root element, so they
outrank the stylesheet's token blocks. `!important` is banned because it
would reverse that. `connectorStudioTheme.test.ts` checks every rule above.
Vitest replaces CSS imports with empty strings, so `vite.config.ts` lets
`connectorStudio.css?raw` through for those tests.

Bundles that ignore the fields keep their built-in theme and stylesheet.

Provider resource reads use release-declared Studio commands. The manifest
pins an HTTPS GET URL, a credential field and scheme, fixed query values,
optional fixed headers, and the only path or query parameters the iframe may
supply. The `bearer` scheme sends `Authorization: Bearer <secret>`. The
`header` scheme sends the raw secret in one named header, such as
`x-goog-api-key`. `fixedHeaders` holds non-secret values such as
`anthropic-version: 2023-06-01`.

Header names must be RFC 7230 tokens. Dex Web compares them
case-insensitively and treats `_` as `-`, because CGI-style servers merge the
two. They cannot be `Authorization`, `Accept`, `Host`, `Content-Length`,
`Transfer-Encoding`, `Connection`, `Keep-Alive`, `TE`, `Trailer`, `Upgrade`,
`Cookie`, `Set-Cookie`, `Origin`, `Referer`, `Forwarded`, `Proxy-*`,
`X-Forwarded-*`, `X-HTTP-Method`, `X-HTTP-Method-Override`, or
`X-Method-Override`. A fixed header cannot repeat the credential header or
another fixed header. Its value must be 1-256 printable ASCII characters
without leading or trailing spaces and must not contain the credential.

Dex Web checks each command's declaration before it sends that command's
request. A command with an unknown credential scheme or an invalid header
fails with `502`, and nothing is sent. The rest of the release still works:
saving credentials, OAuth, UI sessions, and the release's other commands. A
local release override with an invalid declaration fails when Dex Web starts.

Dex Web's generic broker validates the active session and capability, injects
the credential, sends `Accept: application/json`, bounds a JSON-object
response, and rejects responses containing credential material in any JSON
string or object key. It removes spaces around the stored credential before
sending it, because HTTP drops them on the wire, and it checks responses for
the value it sent. The credential must be printable ASCII.
Provider pagination, filtering, and response projection stay in the
Connector-owned UI bundle; Dex Web contains no provider-specific adapters.

Connections persist in `$HOME/.dex/connectors/connections.json` by default.
Use `--connector-config-dir` to select another directory. Restarting Dex Web
reloads the file and cached artifacts. OAuth state, PKCE verifier, client
secret, and UI session nonce remain memory-only and are discarded on restart.
The page displays the absolute JSON path and the
`DEX_CONNECTOR_CONFIG_FILE=... <your-app-command>` launch command. It never
returns credential values. Deleting a local credential does not revoke the
provider grant.

Trigger-binding configuration remains in `connections.json`. Connector Step
operation configuration is stored in the sibling non-secret
`use-configurations.json`, keyed by connector, connection, operation, Flow
type, and Step type. Writes accept only JSON Pointer paths declared by the
Flow definition. Applications load both files as a startup snapshot through
the Connector SDK; changing configuration requires an application restart.

## Trusted reverse-proxy mounts

Dex Web can be mounted at a request-specific path below an authenticated host
application. Enable this only when port 8802 is reachable exclusively from the
trusted reverse proxy:

```yaml
web:
  trustForwardedEmbeddingHeaders: true
```

The equivalent environment variable is
`DEX_WEB_TRUST_FORWARDED_EMBEDDING_HEADERS=true`. The default is false. When it
is false, Dex rejects requests containing any forwarded embedding header.

The proxy strips client-supplied values, removes its public mount prefix before
forwarding, and injects exactly one value for each required header:

```text
X-Forwarded-Prefix: /apps/project-1/dex
X-Dex-Web-Embedded: true
X-Dex-Web-CSRF-Token: host-generated-token
```

`X-Forwarded-Prefix` is the browser-visible absolute path. It is `/` or a
canonical path without a trailing slash, query, fragment, authority, dot
segment, or encoded path separator. `X-Dex-Web-Embedded` accepts only `true` or
`false`. The CSRF bootstrap token is optional to Dex, but hosted deployments
should provide a non-empty, visible-ASCII value.

Dex injects the request-specific base path and presentation mode into the SPA.
The router, assets, navigation links, API calls, and recovery requests use that
path without sharing state between simultaneous mounts. Embedded pages remove
redundant product chrome and allow same-origin framing. Standalone pages deny
framing. HTML is served with `Cache-Control: no-store` and varies on all three
forwarded headers.

For every browser method other than GET, HEAD, or OPTIONS, the SPA copies the
bootstrap token to `X-CSRF-Token`. The host proxy must validate that browser
header against the authenticated session before forwarding the request, then
strip it. Dex transports the token for the host boundary; it does not implement
host authentication, session management, or CSRF validation.

## Dynamic definition bundles

Local and blobstore sources support atomic bundles:

```text
<root>/
  active-manifest
  releases/<release-id>/**/*.json
```

`active-manifest` uses this strict schema:

```json
{
  "schemaVersion": "1.0",
  "releaseId": "550e8400-e29b-41d4-a716-446655440000",
  "bundlePrefix": "_superverse/dex-web/flow-definitions/releases/550e8400-e29b-41d4-a716-446655440000/",
  "bundleDigest": "sha256:<64 lowercase hex characters>",
  "definitionCount": 2
}
```

For a local source, `bundlePrefix` is
`releases/<release-id>/`. For blobstore it includes the configured root prefix as in
the example. Release directories are immutable: upload and verify every JSON
object before replacing `active-manifest`. Dex validates the UUID, exact
prefix, file count, every FDG, and the digest before installing a snapshot.

The digest input is the JSON files sorted by slash-separated relative path.
Each path and exact payload is framed as an unsigned 64-bit big-endian byte
length followed by those bytes; SHA-256 is computed over the concatenated
frames and encoded as lowercase `sha256:<hex>`.

A blobstore source reuses one existing S3 `blobStore.supportedStorages` entry:

```yaml
web:
  flowRenderingSource: blobstore
  flowRenderingBlobStore:
    storageId: p0
    prefix: _superverse/dex-web/flow-definitions
  workQueuePermissionMode: trusted-header
```

It conditionally reads the manifest by ETag on every definition-dependent
request and reuses only an unchanged, already validated snapshot. A changed,
incomplete, or invalid release returns 503 instead of serving the previous
catalog as current. Environment variables override YAML:

```text
DEX_WEB_FLOW_RENDERING_SOURCE
DEX_WEB_FLOW_RENDERING_DIRECTORY
DEX_WEB_FLOW_RENDERING_STORAGE_ID
DEX_WEB_FLOW_RENDERING_PREFIX
DEX_WEB_WORK_QUEUE_PERMISSION_MODE
DEX_WEB_START_FLOW_WORKER_TARGET_HEADLESS
DEX_WEB_TRUST_FORWARDED_EMBEDDING_HEADERS
```

`GET /api/flow-definitions` and `GET /api/v2/catalog` return the active revision
as `ETag`; the v2 catalog also returns `definitionRevision`. The browser sends
that revision as `X-Dex-Flow-Definition-Revision` for Search, Display, edits,
and Actions. A stale request receives `409 FLOW_DEFINITION_CHANGED`; the UI
reloads the catalog, clears stale operation state, and requires Action
confirmation again.

## Run through the Dex Server image

The `dex-server` Docker image embeds these production assets and starts Dex Web,
API, and Interpreter in one process by default. It listens for Web traffic on
port 8802 and FlowService traffic on port 8801.

Run only Web when it needs to scale independently:

```bash
docker run IMAGE --services web
```

Set `web.flowServiceTarget` in the mounted server YAML to the API service
address. Web starts even when that upstream is unavailable. `/healthz` reports
process liveness. `/readyz` validates the current definition snapshot and a
FlowService search, returning `definitionRevision`, source, and definition
count; `/api/*` returns an upstream error until FlowService is available.

To populate Web with a 90-execution Flow containing serial, fan-out, and fan-in
sections, run the [Large Step Graph demo](./demo/large-step-graph).
To exercise the widest layout, run the [90-way fan-out demo](./demo/fan-out-90),
which creates `Step1`, 90 parallel Steps, and no close-decision graph edges.

## Frontend development

Requirements are Node.js 22+ and a local `dexcli` build.

Terminal one starts the Go API bridge on port 8902:

```bash
./cli/dexcli dev --web-port 8902
```

Terminal two starts Vite with hot reload on port 8802:

```bash
cd web
npm install
npm run dev
```

Vite proxies `/api/*` and `/healthz` to `http://127.0.0.1:8902`.

## Build

```bash
cd web
npm ci
npm run check
npm run build
GOWORK=off go test ./...
```

Vite writes production assets to `assets/dist/`. `assets/embed.go` embeds that
directory into the Go module and ultimately into `dexcli`.

## Pages

The v2 list compiles typed filter controls into visibility queries,
loads current-run Summary RPCs with bounded concurrency, and displays Indexed
Attributes before ordered Summary fields. Selecting a run validates the Display
contract, supports inline edits for declared primitive fields, and renders
conditional none/object Actions beside the Flow Definition Graph.
Attribute-sourced Action inputs remain hidden. The UI condition is
presentational; Action RPCs must re-check current state.
Version 2 definitions use `uiSlot` for reusable display placement and expose
each Action's `requiredPermission` from the Go SDK Action registration.

v1 pages live under `/v1/flows` and `/v1/rendering`. The Flows page provides Basic and Advanced visibility queries, pagination,
saved queries, configurable columns, Indexed Attributes, and timezone
preferences.

The Flow Rendering page displays the currently active source definition JSON. Its
legend independently filters control flow, WaitFor, RPCs, resources, SubFlows,
and diagnostics. Control flow, WaitFor, RPCs, Channels, Attributes, SubFlows,
and diagnostics start visible. Streams start hidden. Flow timeout handlers are
always visible as part of the Flow rather than a legend layer.

The Flow Definition renderer uses a compound layout separate from the runtime
Execution graph. The lavender Flow frame contains blue Step frames. Each Step
stacks orange WaitFor paths above its Execute decisions, with dispatch diamonds
for conditional returns. Channels occupy the upper-left resource rail,
Attributes share one box, RPC hexagons cross the Flow's left boundary, and
folded SubFlows occupy the right rail. Publishers point to Channels, Channels
point to consuming WaitFor paths, Attribute writers point to the Attribute box,
and the box points to readers. Channel and Attribute relations are hidden until
their resource or a related Step or RPC is selected. Resource and SubFlow
relations are dashed.
Transitions are solid; Execute-failure recovery is solid magenta.
Long labels wrap inside their shapes. Backward transitions and self-loops use
outer routing lanes. Select an edge to emphasize it and show its complete
condition beside the path; its endpoints and source location remain below the
canvas. The Mini Map starts collapsed. Flow timeout handlers use a compact
Step-like card with their timeout decision, rather than the RPC hexagon.
Viewport controls use visible Zoom In, Zoom Out, and Fit View labels. The
collapsed Mini Map uses a visible Show Mini Map button.
Each graph fits the complete definition into the viewport when it first loads
and after its visible layers change.

Flow Definition Graph 2.0 on **v1** Flow Rendering uses Process Canvas. **v2**
renders the same JSON with the Control Topology canvas. Version 1 continues to
use the original definition renderer on **v1**.

The graph contract, compound layout, React components, and styles live in the
shared [`flow-definition-renderer`](../packages/flow-definition-renderer)
package. Product docs import the same renderer for checked-in Python example
graphs.

The Run page remembers the last selected Overview, Execution graph, Timeline, or
Streams tab across browser refreshes. It automatically loads the complete semantic
history for the selected run. A continued-as-new run remains on screen until the
operator follows **Next run**; Dex Web never combines two runs into one graph or timeline.
The page also provides Live Flow State beside Selected event, Run input beside Identity,
attributes, timers, queued
steps, channels, completed outputs, stop, and time travel. Timeline and Execution graph keep
Selected event in the sidebar, where the critical-action **Review time travel**
entry opens the operation with that Step execution and its WaitFor or Execute
method already selected. Backend history IDs remain internal pagination and
correlation details. A Time Travel-created run includes a **TimeTravelFork**
event whose source run links back to the preserved history.
AttributeMap and ChannelMap instances with decimal-only names appear before
other instances and sort by numeric value in live state and event details.
Continued runs link to their previous run from Timeline and Execution graph.
Timeline connects each Step execution's first method event to the Flow start,
Flow continued, RPC, Step decision, or recovery event that scheduled it.
Selecting the first event reveals that source link; selecting a
WaitFor event also reveals its outgoing WaitFor-to-Execute link.
Execution graph nodes separate WaitFor and Execute with distinct colors, channel names, condition icons, and individual event details.
Selecting a Step execution emphasizes it in green, its previous Step in blue, its next Steps in orange, and their connecting arrows.
Close decisions remain in event details and do not create misleading Execution graph edges.
Large Execution graphs extend down the page at readable size. Fan-out ranks spread
horizontally and scale down to fit when possible, with a minimum one-third zoom.
Persisted Step decisions add planned graph branches. A selector-matched branch without a Step event appears Canceled with no execution ID.
SubFlow conditions appear as linked leaf nodes and compact WaitFor cards. Running
and terminal nodes display and link by their generated Flow ID. Original WaitFor
events retain the configured reuse policy; continue-as-new state retains only
the Condition ID and stable list position.
Methods interrupted by a forced close remain visible as Pending Timeline events
with their last persisted Scheduled or Started phase.
Timeline and Execution graph share structured event details for flow, step method, RPC,
and channel events. A Raw JSON tab preserves the complete server payload.
Successful Temporal RPC Updates appear as the same RPC events as result signals.
Blob-backed RPC input and output hydrate through the existing selected-event loader.
The Streams tab accepts a Stream name, reads its retained messages from the beginning,
and then continuously long-polls for the next message. Each message displays its creation
time and source. Stream retention is best effort, so older messages can be trimmed.
External attribute writes appear as Attributes updated events, with a
SetAttributes type label and the changed values in the event details.
Raw JSON shows hydrated values; missing retained data is labeled unavailable.
SYNC step Context shows the immediately preceding retry failure when available,
including its backend-native error, worker details, named and numeric gRPC
status, and an optional Worker-provided stack. Live Flow State shows the latest
pending failure on its matching active Step and expands that stack by default.
Selected event Context uses the same failure view with the stack collapsed.
## License

[Sustainable Use License 1.0](LICENSE), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
