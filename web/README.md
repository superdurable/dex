# Dex Web

Dex Web searches flows and displays Dex semantic history, step topology,
current interpreter state, and reset points.

The production Web server is Go. It serves an embedded React SPA and translates
same-origin HTTP/JSON requests under `/api/` to Dex `FlowService` gRPC calls.
Temporal/Cadence credentials and backend history never enter the browser.

`GetHistoryEvents` returns the same complete step event shape for sync and async
durability. `POST /api/blobs/load` batches Dex `LoadBlobs` calls. The browser
recursively hydrates the selected event and current flow state, dedupes by blob
kind and ID, and caches loaded values across tabs.
Missing values are labeled `Value blob unavailable`; missing local-activity
requests are labeled `Step event input blob unavailable`. Neither exposes blob
IDs or storage paths.

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
saved queries, configurable columns, custom search attributes, and timezone
preferences.

The Run page provides Overview, Step graph, Timeline, active/waiting state,
attributes, timers, queued steps, channels, completed outputs, and reset.
Continued runs link to their previous run from Timeline and Step graph.
Step graph nodes separate WaitFor and Execute with distinct colors, channel names, condition icons, and individual event details.
Timeline and Step graph share structured event details for flow, step method, RPC,
and channel events. A Raw JSON tab preserves the complete server payload.
Raw JSON shows hydrated values; missing retained data is labeled unavailable.
## License

[Super Durable Source License 1.0](LICENSE), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
