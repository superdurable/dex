# DEX WebUI

A minimal Next.js + Tailwind WebUI with two pages:

- **List** (`/`) — calls `OpsService.ListRuns` to show recent runs for a
  namespace.
- **Show** (`/flow/show?namespace=...&runId=...`) — combines two RPCs:
  `OpsService.GetHistoryEvents` for the timeline / step graph, and
  `RunsService.GetRun` (auto-polled every 2s while non-terminal) for the
  authoritative live status, current state map, and pending unconsumed
  channel messages shown in the left rail.

The browser never speaks gRPC: Next.js API routes under `app/api/runs/...`
act as a Backend-For-Frontend, dynamically loading
`protocol-grpc/protos/dex.proto` via `@grpc/proto-loader` and dialing
`OpsService` and `RunsService` via `@grpc/grpc-js`.

## Quick start

The local stack (Postgres + Mongo single-node RS + dex-server + benchmark
worker) runs in Docker via [`docker-compose.e2e.yaml`](./docker-compose.e2e.yaml).
The server defaults to the Postgres backend; set `DEX_DB_BACKEND=mongo` to run
against Mongo instead (both DBs are started either way).

```bash
# 1. Bring the stack up and seed some runs (from the repo root).
../dev-stack.sh

# 2. Install JS deps and start Next.js.
npm install
OPS_SERVICE_ADDRESS=127.0.0.1:7235 RUN_SERVICE_ADDRESS=127.0.0.1:7233 npm run dev

# 3. Open the browser.
open http://localhost:3000/?namespace=default
```

(Both env vars default to those addresses if omitted, so the bare
`npm run dev` works for the standard dev-stack.)

Tear down with:

```bash
docker compose -f docker-compose.e2e.yaml -p dex-web-e2e down -v
```

The benchmark worker exposes an HTTP trigger on `:9123` so you can produce
more runs at any time:

```bash
curl 'http://127.0.0.1:9123/trigger?mode=parallel&runs=5&numSteps=4&stateSize=128'
```

## Configuration

- `OPS_SERVICE_ADDRESS` — gRPC address for the OpsService (read-only ops /
  visibility surface). Default `127.0.0.1:7235`. Used by `/api/runs/list`
  and `/api/runs/history`.
- `RUN_SERVICE_ADDRESS` — gRPC address for the RunsService. Default
  `127.0.0.1:7233`. Used by `/api/runs/get` for live run state polling on
  the show page.
- `DEX_PROTO_PATH` — absolute path to `dex.proto`. Defaults to
  walking up from `process.cwd()` to find `protocol-grpc/protos/dex.proto`.

## Layout

```
web/
├─ app/
│  ├─ api/
│  │  ├─ _grpc/
│  │  │  ├─ client.ts           # proto-loader + grpc-js singleton
│  │  │  └─ mappers.ts          # FlowRun + HistoryEvent JSON shapes
│  │  └─ runs/
│  │     ├─ list/route.ts       # POST -> ListRuns
│  │     ├─ get/route.ts        # GET  -> RunsService.GetRun (live state)
│  │     └─ history/route.ts    # GET  -> GetHistoryEvents (auto-paged)
│  ├─ components/                # Header, StatusBadge, EventCard, utils
│  ├─ flow/show/page.tsx         # Show page (timeline + graph + live state panel)
│  ├─ flow/show/RunStatePanel.tsx # Left rail: live status / state / pending channels
│  ├─ page.tsx                   # List page (table)
│  ├─ layout.tsx
│  └─ globals.css
├─ docker-compose.e2e.yaml       # postgres + mongo + dex-server + benchmark
├─ ../dev-stack.sh               # boots compose + triggers seed runs (repo root)
├─ e2e/                          # Playwright tests
│  ├─ globalSetup.ts             # docker compose up + seed runs
│  ├─ globalTeardown.ts          # optional docker compose down -v
│  ├─ list.spec.ts
│  └─ show.spec.ts
├─ playwright.config.ts
├─ package.json
└─ tsconfig.json
```

## End-to-end tests

```bash
npm install
npx playwright install chromium

# Full lifecycle: brings the docker stack up, seeds runs, runs specs.
# The stack stays up afterward so local dev-stack is not disrupted.
npm run e2e

# Iterate on specs against an already-up stack (from repo root):
../dev-stack.sh
E2E_SKIP_STACK=1 npm run e2e

# Tear the stack down after tests (CI does this via workflow; opt-in locally):
E2E_TEARDOWN_STACK=1 npm run e2e
```

The Playwright suite asserts on `data-testid` hooks (`runs-table`,
`run-row`, `run-link`, `status-badge`, `timeline`, `event-card`,
`data-event-type`) — selectors are resilient to Tailwind class changes.
