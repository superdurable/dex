# Dex Web

The live Flow overview lists pending Channel messages with their server-assigned
IDs and decoded values. Operators can delete a pending message. Temporal performs
the deletion atomically; the Cadence query-plus-signal fallback is best effort and
can race message consumption.

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

To load static Flow Definition Graph JSON files into the independent **Flow
Rendering** page, provide a directory at startup:

```bash
dexcli dev --flow-rendering-dir ./build/flow-definitions
```

Dex scans JSON files recursively and takes a snapshot at startup. Generate them
with `dexcli visualize SOURCE --json --out ./build/flow-definitions/name` and restart
Dex after changing the files. Invalid JSON, unsupported schema versions, or a
non-directory path stop startup with an error.

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

The Flows page provides Basic and Advanced visibility queries, pagination,
saved queries, configurable columns, Indexed Attributes, and timezone
preferences.

The Flow Rendering page displays source definition JSON loaded at startup. Its
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

The graph contract, compound layout, React components, and styles live in the
shared [`flow-definition-renderer`](../packages/flow-definition-renderer)
package. Product docs import the same renderer for checked-in Python example
graphs.

The Run page opens on Execution graph and also provides Overview (Live Flow State beside
Selected event, then Run input beside Identity), Timeline, Streams, attributes, timers, queued
steps, channels, completed outputs, stop, and time travel. Timeline and Execution graph keep
Selected event in the sidebar, where the critical-action **Review time travel**
entry opens the operation with that Step execution and its WaitFor or Execute
method already selected. Backend history IDs remain internal pagination and
correlation details. A Time Travel-created run includes a **TimeTravelFork**
event whose source run links back to the preserved history.
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

[Super Durable Source License 1.0](LICENSE), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
