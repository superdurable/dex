# Dex Web

Dex Web searches flows and displays Dex semantic history, step topology,
live flow state, reset points, and stop controls.

The production Web server is Go. It serves an embedded React SPA and translates
same-origin HTTP/JSON requests under `/api/` to Dex `FlowService` gRPC calls.
Temporal/Cadence credentials and backend history never enter the browser.

`GetHistoryEvents` returns the same `input`, `output`, and `context` step-event
shape for sync, async, and async-fallback execution. Timeline, Step graph,
Reset, and Selected event use that common structure.

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

The Run page provides Overview (Live Flow State beside Selected event, then
Run input beside Identity), Step graph, Timeline, attributes, timers, queued
steps, channels, completed outputs, stop, and reset. Timeline and Step graph keep
Selected event in the sidebar.
Continued runs link to their previous run from Timeline and Step graph.
Timeline connects each Step execution's first method event to the Flow start,
Flow continued, RPC, Step decision, transient movement, or recovery event that
scheduled it. Selecting the first event reveals that source link; selecting a
WaitFor event also reveals its outgoing WaitFor-to-Execute link.
Step graph nodes separate WaitFor and Execute with distinct colors, channel names, condition icons, and individual event details.
SubFlow conditions appear as linked leaf nodes and compact WaitFor cards. Running
and terminal nodes link by their generated Flow ID. Cards and nodes show the
configured reuse policy.
Methods interrupted by a forced close remain visible as Pending Timeline events
with their last persisted Scheduled or Started phase.
Timeline and Step graph share structured event details for flow, step method, RPC,
and channel events. A Raw JSON tab preserves the complete server payload.
Successful Temporal RPC Updates appear as the same RPC events as result signals.
Blob-backed RPC input and output hydrate through the existing selected-event loader.
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
