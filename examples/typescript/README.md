# Dex TypeScript examples

These examples target [`@superdurable/dex@0.2.3`](https://www.npmjs.com/package/@superdurable/dex).

The sample process hosts one gRPC Worker (default `127.0.0.1:8803`) and an HTTP
controller on port `8080`. Step `execute` / `waitFor` / RPC handlers may
`await` the shared `Client` directly (async await on the Worker). Disk
BlobCaches are under `DEX_BLOB_CACHE_DIR`.

Controllers handle expected duplicate and missing-Flow failures through
`DexServiceError` gRPC codes; no example compares Dex sub-status metadata.

## Layout

```
src/
├── products/       # real-world business scenarios
├── patterns/       # design patterns
├── primitives/     # one minimal example per Dex primitive
├── config/         # env and Cron schedule bootstrap
└── main.ts         # Worker and HTTP entrypoint
```

HTTP routes use category prefixes:

- `/products/<kebab>/...` — e.g. `/products/job-post/create`
- `/patterns/<kebab>/...` — e.g. `/patterns/polling/start/simple`
- `/primitives/<kebab>/...` — e.g. `/primitives/channel/approve`

## Run locally

```bash
dexcli dev
cd examples/typescript
npm install
npm start
```

The Worker synchronizes all registered Indexed Attributes with Dex before it
opens its listener; no backend CLI registration is required.

Use Node.js 22 or 24. Defaults connect to Dex at `localhost:8801`. Override with
`DEX_FLOW_SERVICE_ADDRESS`, `DEX_WORKER_BIND_ADDRESS`, `DEX_WORKER_TARGET`,
`DEX_EXAMPLES_HTTP_ADDRESS`, `DEX_BLOB_CACHE_DIR`.

## Verify

```bash
npm test                 # SubscriptionBilling unit tests
npm run test:integ       # product integ tests (requires Dex)
npm run smoke            # every product + pattern HTTP route
./run-integration-tests.sh # start dexcli dev and run both integration suites
```

The integration suite starts and verifies Money Transfer, Order Processing,
Engagement, Microservice, Polling, Subscription, and Failure Recovery Flows.

The Go examples support `./run-e2e-tests.sh --keep-running` to leave Dex running
after E2E tests for manual HTTP exploration.

## Products

- [Money transfer](./src/products/money-transfer)
- [Order processing](./src/products/order-processing)
- [Microservice orchestration](./src/products/microservices)
- [Engagement](./src/products/engagement)
- [Subscription](./src/products/subscription)
- [Polling](./src/products/polling)
- [Signup](./src/products/signup)
- [Job post](./src/products/job-post)
- [Shortlist candidates](./src/products/shortlist-candidates)

## Patterns

Under [`src/patterns/`](./src/patterns):

- Cron schedule
- Drain internal / externally published channels
- Interruptible execution
- Manual recovery
- Parallel Steps (static / dynamic / await / first win)
- Parent–child
- Polling (simple / backoff)
- Failure recovery (saga)
- Reminders
- Resettable timer
- Scalable parallel
- [Entity Store user profiles](./src/patterns/entity-store) ([PostgreSQL setup](../entity-store))
- Timeout handling
- Wait for state completion

## Primitives

Seven minimal examples under [`src/primitives/`](./src/primitives/): step,
attribute, channel, timer, rpc, subflow, and client-apis.
