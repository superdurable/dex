# Dex TypeScript examples

These examples target [`@superdurable/dex@0.1.3`](https://www.npmjs.com/package/@superdurable/dex).

The sample process hosts one gRPC Worker (default `127.0.0.1:8803`) and an HTTP
controller on port `8080`. Step `execute` / `waitFor` / RPC handlers may
`await` the shared `Client` directly (async await on the Worker). Disk
BlobCaches are under `DEX_BLOB_CACHE_DIR`.

## Run locally

```bash
dexcli dev --temporal-db-filename /tmp/dex-examples.db

# Required once per Temporal namespace:
temporal --address 127.0.0.1:7233 operator search-attribute create \
  --name ActiveStepTypes --type KeywordList
temporal --address 127.0.0.1:7233 operator search-attribute create \
  --name CustomKeywordField --type Keyword
temporal --address 127.0.0.1:7233 operator search-attribute create \
  --name CustomStringField --type Text

cd examples/typescript
npm install
npm start
```

Use Node.js 22 or 24. Defaults connect to Dex at `localhost:8801`. Override with
`DEX_FLOW_SERVICE_ADDRESS`, `DEX_WORKER_BIND_ADDRESS`, `DEX_WORKER_TARGET`,
`DEX_EXAMPLES_HTTP_ADDRESS`, `DEX_BLOB_CACHE_DIR`.

## Tests

```bash
npm test                 # SubscriptionBilling unit tests
npm run test:integ       # product integ tests (requires Dex)
npm run smoke            # every product + design-pattern HTTP route
```

## Product examples

- [Money transfer](./src/workflow/money/transfer)
- [Microservice orchestration](./src/workflow/microservices)
- [Engagement](./src/workflow/engagement)
- [Subscription](./src/workflow/subscription)
- [Polling](./src/workflow/polling)
- [Signup](./src/workflow/signup)
- [Job post](./src/workflow/jobpost)
- [Shortlist candidates](./src/workflow/shortlistcandidates)

## Design patterns

All under [`patterns/workflow`](./src/patterns/workflow),
HTTP under `/design-pattern/...`:

- Cron schedule
- Drain internal / signal channels
- Interruptible execution
- Manual intervention
- Parallel states (simple / with await)
- Parent–child
- Polling (simple / backoff)
- Failure recovery (saga)
- Reminders
- Resettable timer
- Scalable parallel
- Storage singleton
- Timeout handling
- Wait for state completion
