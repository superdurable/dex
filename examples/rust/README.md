# Rust examples

This application mirrors the examples shared by the Java, Python, and TypeScript
applications. It intentionally depends on the published `dex-sdk = "=0.2.3"`
crate without a repository path override.

## Layout

```
src/
├── products/       # real-world business scenarios
├── patterns/       # design patterns
├── primitives/     # one minimal example per Dex primitive
└── server/         # Axum HTTP router
```

HTTP routes use category prefixes:

- `/products/<kebab>/...` — e.g. `/products/job-post/create`
- `/patterns/<kebab>/...` — e.g. `/patterns/polling/start/simple`
- `/primitives/<kebab>/...` — e.g. `/primitives/channel/approve`

## Run

Rust 1.97 or newer is required. Start Dex, then run the Worker and HTTP server:

```bash
cd examples/rust
dexcli dev
cargo run --locked
```

The Worker connects to `127.0.0.1:8801`, listens on `127.0.0.1:8803`, serves HTTP
on `127.0.0.1:8080`, and stores large payload blobs under
`/tmp/dex-rust-examples-blobs`. Override these with `DEX_SERVER_ADDRESS`,
`DEX_WORKER_BIND_ADDRESS`, `DEX_EXAMPLES_HTTP_ADDRESS`, and `DEX_BLOB_CACHE_DIR`.

## Products

Every shared product example has a distinct Rust Flow and implementation file.

| Java/Python/TypeScript example | Rust Flow | Demonstrated SDK features |
|---|---|---|
| Money transfer | [`MoneyTransferFlow`](src/products/money_transfer/flow.rs) | Debit/credit saga, Execute retry, and compensation |
| Order processing | [`OrderProcessingFlow`](src/products/order_processing) | Charge, seller Channel + reminder Timer, ship retry, refund |
| Microservice orchestration | [`OrchestrationFlow`](src/products/microservices.rs) | Parallel Steps, Attribute swap RPC, Channel-or-timer wait |
| Engagement | [`EngagementFlow`](src/products/engagement.rs) | Indexed status, decision RPCs, reminders, external notification |
| Subscription | [`SubscriptionFlow`](src/products/subscription.rs) | Billing timers, concurrent control Step, update/cancel RPCs |
| Signup | [`UserSignupFlow`](src/products/signup.rs) | Verification Channel and recurring reminder timer |
| Job posting | [`JobPostingFlow`](src/products/job_post/flow.rs) | Indexed CRUD with parallel LinkedIn and Indeed updates |
| Shortlist candidates: employer opt-in | [`EmployerOptInFlow`](src/products/shortlist_candidates.rs) | Long-running opt-in state and opt-out Channel |
| Shortlist candidates: shortlist | [`ShortlistFlow`](src/products/shortlist_candidates.rs) | Scheduled contact or revoke race |

## Patterns

Every shared design-pattern Flow is independently registered; multi-Flow patterns
remain split so their orchestration boundaries are visible.

| Java/Python/TypeScript pattern | Rust Flow | Demonstrated SDK features |
|---|---|---|
| Cron schedule | [`CronScheduleFlow`](src/patterns/cron) | Fixed-interval durable timer loop |
| Drain internal Channel | [`DrainInternalChannelFlow`](src/patterns/drain_channels.rs) | Internal publication, one-at-a-time drain, sentinel completion |
| Draining External Channel Publishing | [`DrainingExternalChannelFlow`](src/patterns/drain_channels/flow.rs) | RPC publication, one-at-a-time drain, conditional completion |
| Interruptible execution | [`InterruptibleFlow`](src/patterns/interruptible/flow.rs) | Graceful interrupt handling |
| Manual recovery | [`ManualRecoveryFlow`](src/patterns/intervention/flow.rs) | Exhausted retry recovery and manual Channel decision |
| Static parallel Steps | [`StaticParallelStepsFlow`](src/patterns/parallel/parallel_step_flows.rs) | A fixed set of concurrent Step types |
| Dynamic parallel Steps | [`DynamicParallelStepsFlow`](src/patterns/parallel/parallel_step_flows.rs) | A runtime-sized set of one Step type |
| Await parallel Steps | [`AwaitParallelStepsFlow`](src/patterns/parallel/parallel_step_flows.rs) | Wait for every worker's Channel message |
| First-win parallel Steps | [`FirstWinParallelStepsFlow`](src/patterns/parallel/parallel_step_flows.rs) | Keep the first result and cancel siblings |
| Basic SubFlow fan-out | [`BasicParentFlow`](src/patterns/parallel_subflows/flow.rs) | Wait for half of the SubFlows and cancel the remaining IDs |
| Long-lived SubFlow parent | [`AdvancedLongLiveParentFlow`](src/patterns/parallel_subflows/flow.rs) | Bounded workers, request Channel, stop Attribute, and SubFlow loop |
| Short-lived SubFlow parent | [`AdvancedShortLiveParentFlow`](src/patterns/parallel_subflows/flow.rs) | Locked active count and atomic completion when the Channel is empty |
| Partitioning and back pressure | [`SubmitRequestFlow`](src/patterns/parallel_subflows/flow.rs) | Stable parent partitioning and durable retries after admission rejection |
| Simple polling | [`SimplePollingFlow`](src/patterns/polling.rs) | Durable timer loop |
| Backoff polling | [`BackoffPollingFlow`](src/patterns/polling.rs) | Execute retry with exponential backoff |
| Failure recovery | [`FailureRecoveryFlow`](src/patterns/recovery.rs) | Retry exhaustion and compensation Step |
| Reminders | [`ReminderFlow`](src/patterns/reminders.rs) | Reminder loop, accept/opt-out Channels, global timeout |
| Inactiveness Tracker Timer | [`InactivenessTrackerFlow`](src/patterns/inactiveness_tracker/flow.rs) | Activity resets a timer; expiry processes inactiveness |
| Entity Store | [`UserProfileFlow`](src/patterns/entity_store.rs) | Attribute Store projection and profile RPCs |
| Timeout handling | [`FlowGracefulTimeout`](src/patterns/timeout.rs) | Task-versus-timeout race and forced completion/failure |
| Wait for Step completion | [`WaitForStepCompletionFlow`](src/patterns/wait_for_step_completion.rs) | `Client::wait_for_step_completion` target followed by background work |

The Java retrying-Worker example, Python basic/resource-control/AI-agent examples,
and Go dataset-deal example (separate `dex-dataset-deal` binary) are language-specific
and are not part of the shared cross-language catalog.

## Verify

```bash
make fmt-check
make clippy
make test
./run-integration-tests.sh
```

The catalog integration test constructs every Flow, checks the exact 9 + 23
mapping, rejects duplicate names, validates all definitions in one Registry, and
ensures the manifest uses the published crate rather than a local path.

The integration script starts the current checkout's `dexcli dev`, then starts
and verifies Money Transfer, Engagement, Microservice, Subscription,
and Failure Recovery Flows through the published Rust SDK.

The Go examples support `./run-e2e-tests.sh --keep-running` to leave Dex running
after E2E tests for manual HTTP exploration.
