# Dex Web

Next.js console for searching Dex flows and inspecting Dex semantic history,
step topology, current interpreter state, and reset points.

## Development

Requirements:

- Node.js 22+
- a Dex server listening on plaintext gRPC

```bash
cd web
cp .env.example .env.local
npm install
npm run dev
```

Open [http://localhost:3000](http://localhost:3000). The default server target is
`127.0.0.1:8801`; override it with `DEX_SERVER_ADDRESS`.

The Next.js server reads [`../protos/dex.proto`](../protos/dex.proto) at runtime.
Set `DEX_PROTO_PATH` only when the Web process does not run inside this
repository.

## Pages

The Flows page provides:

- Basic filters that generate a visibility query;
- an Advanced editor for the complete query;
- shareable URL state, recent queries, and named queries;
- configurable columns, custom search attributes, timezone, and token
  pagination.

The Run page provides:

- Overview, Step graph, and Timeline tabs;
- expandable Dex semantic event payloads;
- active/waiting step state, attributes, timers, queued steps, channels, and
  completed outputs;
- native-history pagination followed by `WaitForHistoryEvent` long polling;
- reset by beginning, semantic anchor event, time, step type, or step execution.

The browser calls local Next.js route handlers. Those handlers call Dex
`FlowService` over gRPC, so Temporal/Cadence credentials and internal history
never reach browser code.

## Validation

```bash
npm run typecheck
npm test
npm run build
```

Unit tests cover Basic/Advanced query conversion and the shared SYNC/ASYNC step
lineage model. Browser E2E coverage will use the Temporal and Cadence integration
stacks after the Web module is wired into their test fixtures.
