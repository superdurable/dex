# Rust examples

This application mirrors the examples shared by the Java, Python, and TypeScript
applications. It intentionally depends on the published `dex-sdk = "=0.0.2"`
crate without a repository path override.

## Run

Rust 1.97 or newer is required. Start Dex, then run the Worker:

```bash
cd examples/rust
cargo run --locked
```

The Worker connects to `127.0.0.1:8801`, listens on `0.0.0.0:8803`, and stores
large payload blobs under `/tmp/dex-rust-examples-blobs`. Override these with
`DEX_SERVER_ADDRESS`, `DEX_WORKER_ADDRESS`, and `DEX_BLOB_CACHE_DIR`.

## Product examples

Every shared product example has a distinct Rust Flow and implementation file.

| Java/Python/TypeScript example | Rust Flow | Demonstrated SDK features |
|---|---|---|
| Money transfer | [`MoneyTransferFlow`](src/workflow/money_transfer.rs) | Execute retry and debit compensation |
| Microservice orchestration | [`OrchestrationFlow`](src/workflow/microservices.rs) | Parallel Steps, Attribute swap RPC, Channel-or-timer wait |
| Engagement | [`EngagementFlow`](src/workflow/engagement.rs) | Indexed status, decision RPCs, reminders, external notification |
| Subscription | [`SubscriptionFlow`](src/workflow/subscription.rs) | Billing timers, concurrent control Step, update/cancel RPCs |
| Polling | [`PollingFlow`](src/workflow/polling.rs) | Two external Channels and a timer-driven polling branch |
| Signup | [`UserSignupFlow`](src/workflow/signup.rs) | Verification Channel and recurring reminder timer |
| Job post | [`JobPostFlow`](src/workflow/job_post.rs) | Full-text Attributes and read/update/soft-delete RPCs |
| Shortlist candidates: employer opt-in | [`EmployerOptInFlow`](src/workflow/shortlist_candidates.rs) | Long-running opt-in state and opt-out Channel |
| Shortlist candidates: shortlist | [`ShortlistFlow`](src/workflow/shortlist_candidates.rs) | Scheduled contact or revoke race |

## Design patterns

Every shared design-pattern Flow is independently registered; multi-Flow patterns
remain split so their orchestration boundaries are visible.

| Java/Python/TypeScript pattern | Rust Flow | Demonstrated SDK features |
|---|---|---|
| Cron | [`CronScheduleFlow`](src/patterns/cron.rs) | `StartFlowOptions::cron_schedule` |
| Drain internal Channels | [`DrainInternalChannelsFlow`](src/patterns/drain_channels.rs) | Internal publication, one-at-a-time drain, conditional completion |
| Drain signal Channels | [`DrainSignalChannelsFlow`](src/patterns/drain_channels.rs) | RPC publication, one-at-a-time drain, conditional completion |
| Interruptible execution | [`InterruptibleExecutionFlow`](src/patterns/interruptible.rs) | Handler cancellation and execute timeout |
| Manual intervention | [`ManualInterventionFlow`](src/patterns/intervention.rs) | Exhausted retry recovery and approval Channel |
| Simple parallel states | [`SimpleParallelStatesFlow`](src/patterns/parallel.rs) | Parallel Step movements and graceful completion |
| Parallel states with await | [`ParallelStatesWithAwaitFlow`](src/patterns/parallel.rs) | Independent Channel-gated branches |
| Parent-child | [`ParentFlowV2`](src/patterns/parent_child.rs) | Child ID persistence and completion callback Channel |
| Simple polling | [`SimplePollingFlow`](src/patterns/polling.rs) | Durable timer loop |
| Backoff polling | [`BackoffPollingFlow`](src/patterns/polling.rs) | Execute retry with exponential backoff |
| Failure recovery | [`FailureRecoveryFlow`](src/patterns/recovery.rs) | Retry exhaustion and compensation Step |
| Reminders | [`ReminderFlow`](src/patterns/reminders.rs) | Reminder loop, accept/opt-out Channels, global timeout |
| Resettable timer | [`ResettableTimerFlow`](src/patterns/resettable_timer.rs) | Reset Channel racing an identified timer |
| Scalable parallel: child | [`ChildFlow`](src/patterns/scalable_parallel.rs) | Independently scalable task processor |
| Scalable parallel: parent | [`ParentFlow`](src/patterns/scalable_parallel.rs) | Bounded durable queue and task dispatch |
| Scalable parallel: request receiver | [`RequestReceiverFlow`](src/patterns/scalable_parallel.rs) | Durable ingress buffer and forwarding loop |
| Entity Store | [`UserProfileFlow`](src/patterns/entity_store.rs) | Attribute Store projection and profile RPCs |
| Timeout handling | [`FlowGracefulTimeout`](src/patterns/timeout.rs) | Task-versus-timeout race and forced completion/failure |
| Wait for state completion | [`WaitForStateCompletionFlow`](src/patterns/wait_for_state_completion.rs) | `Client::wait_for_step_completion` target followed by background work |

The Java retrying-Worker example, Python basic/resource-control/AI-agent examples,
and Go dataset-deal example are language-specific and are not part of the shared
cross-language catalog.

## Verify

```bash
make fmt-check
make clippy
make test
```

The catalog integration test constructs every Flow, checks the exact 9 + 19
mapping, rejects duplicate names, validates all definitions in one Registry, and
ensures the manifest uses the published crate rather than a local path.
