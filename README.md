# Durable Execution (DEX), Redefined

This repository includes:

- [server/](server/) — gRPC APIs, workflow engine, and persistence
- [sdk-go/](sdk-go/) — Go SDK for workers and workflow authors
  - [examples-go/](examples-go/) — sample workflows (counter, order tracking, subscription)
- [docs/](docs/) — architecture and subsystem design notes
- [benchmark/](benchmark/) — benchmark worker and load-test flows
- [deploy/](deploy/) — Helm charts and deployment scripts (Kind, GKE)
- [web/](web/) — Web UI (Next.js BFF → OpsService gRPC; see [web/README.md](web/README.md))

## Development plan

### 1.0 features
- [x] Sequential & Parallel steps
- [x] StartRun API
- [x] Basic State schema(no dynamic)
- [x] StopRun API
- [x] Basic visibility(list) APIs &mdash; see [visibility-store-design](docs/visibility-store-design.md), [ops-service-design](docs/ops-service-design.md)
- [x] History APIs: `GetHistoryEvents` &mdash; see [history-store-design](docs/history-store-design.md), [ops-fifo-queue-design](docs/ops-fifo-queue-design.md).
- [x] WebUI: List & Show views &mdash; see [web/README.md](web/README.md)
- [x] Basic channel &mdash; see [wait-for-conditions-design](docs/wait-for-conditions-design.md)
- [x] Durable timer &mdash; see [wait-for-conditions-design](docs/wait-for-conditions-design.md)
- [x] AnyOf & AllOf &mdash; see [wait-for-conditions-design](docs/wait-for-conditions-design.md)
- [x] Dynamic channel &mdash; see [wait-for-conditions-design#dynamic-channels](docs/wait-for-conditions-design.md#dynamic-channels)
- [x] CancelSiblingStepExecution &mdash; see [cancel-sibling-step-execution-design](docs/cancel-sibling-step-execution-design.md)
- [x] Step Timeout
- [x] Step Durable Backoff retry and last failure info
- [x] SAGA: ProceedToStepOnRetryExhausted
- [x] Dynamic state keys
- [x] WaitForHistoryEvent API &mdash; see [wait-for-history-design](docs/wait-for-history-design.md)
- [x] WaitForRunComplete API &mdash; see [wait-for-history-design](docs/wait-for-history-design.md)
- [x] Time travel API &mdash; see [time-travel-api-design](docs/time-travel-api-design.md)
- [ ] State snapshot for step execution
- [ ] GetHistory API with blob reuse to save the data transportation
- [ ] State keys locking
- [ ] DeleteRun API
- [ ] PruneRunHistory API
- [ ] WaitForStepComplete API
- [ ] Start with state
- [ ] Start with runID Reuse
- [ ] BlobStore
- [ ] FlowGraph view
- [ ] API ratelimit and task rate limit
- [ ] External DB integration with state Keys & vector store
- [ ] RPC
- [ ] Rust SDK core and Python SDK
- [ ] Visibility: search by retrying steps filters
- [ ] Visibility: search by waitingFor steps filters(versioning helper)
- [ ] Visibility: search on state values

## 1.0+ features
- [ ] TypeScript SDK
- [ ] Java SDK
- [ ] Auto Delete & Prune after run closed(per closed status)
- [ ] Channel draining
- [ ] SkipTimer API
- [ ] Channel size limit
- [ ] Channel message uniqueness 
- [ ] Async step durability
- [ ] Count API and webUI
- [ ] ChildRun for fanout
- [ ] StepOptionsOverride 
- [ ] AnyCombinationsOf
- [ ] Encryption example
- [ ] Handoff to a different worker for step/flow undefined error
- [ ] Frozen state keys for step execution

## Future internal optimization 
- [ ] Pickup improvement - PollForRun with runID and graceful handoff
- [ ] Review and simplify metrics & docs


## Contribution Guide

### Plan-mode enforcement

When the agent uses the `CreatePlan` tool, every plan must include five top-level sections: `## Tests`, `## Metrics`, `## Logging`, `## Documentation`, and `## UI/UX`. The canonical list lives in [.cursor/rules/plan-mode-checklist.mdc](.cursor/rules/plan-mode-checklist.mdc) and is backed by per-area `alwaysApply` rules under [.cursor/rules/](.cursor/rules/).

A `postToolUse` hook ([.cursor/hooks.json](.cursor/hooks.json) + [.cursor/hooks/check-plan-sections.sh](.cursor/hooks/check-plan-sections.sh)) runs after each `CreatePlan` call and emits a warning into the agent's next turn when any required section is missing. The hook is **advisory only** — it never blocks the call — but the agent should treat the warning as a hard error and edit the plan immediately.

If a section truly does not apply (e.g., a tooling-only change has no UI surface), write `N/A: <one-line reason>` under that header. The hook accepts this as satisfying the requirement.