# TypeScript error handling

Before designing sequential writes and reads, use the [shared read-after-write matrix](../core/read-after-write.md). A typed read-only RPC or `client.getAttributes` call issued after a successful direct-state RPC write observes that write when it reads the written state; search indexes, Attribute Store projections and triggered business completion are separate. Follow this page for the language-specific error and timeout model.

## Worker handler failures

Throw or reject when the method attempt must fail. Configure `waitForRetry` and `executeRetry` independently and route exhausted failures with `WaitForFailure` or `ExecuteFailure`. A recovery Step reads `context.recoveryError` and must be registered in the Flow's StepList. The TypeScript SDK has no time-limit predicates at its v1.20.0 baseline, so branch only on error types the application defines and never on a fixed Dex time-limit spelling; see [recovery error types](../core/error-handling.md#recovery-error-types).

Do not catch and return a success decision merely to suppress retry. That commits staged mutations. External calls are outside the Dex commit, so use stable idempotency keys and compensation for ambiguous outcomes.

## Client failures

Catch exported SDK error classes such as `FlowAlreadyStartedError`, `FlowNotFoundError`, `FlowNotActiveOrNotFoundError`, and `RequestTimeoutError`. Durable Step and Attribute waits never expose transport long-poll expiry; they reattach transparently and preserve the total `requestTimeoutMs` budget. `RequestTimeoutError` means that caller-visible budget expired, not that the Flow or the wait condition failed. For lower-level cases, `DexServiceError` exposes the gRPC code and diagnostic detail. Do not compare human-readable detail for normal control flow when a typed error exists.

Concrete remote Client errors extend `DexServiceError`; local definition, value-mapping, argument, and programming errors do not. In a `catch` block, keep the value `unknown` and narrow with `instanceof`. Catch `DexServiceError` only at a boundary whose policy intentionally treats every remote Dex outcome the same; ordinary domain logic should match the concrete exported class it can decide.

When the API promises a critical operation has completed, follow the shared [required business milestone rule](../core/error-handling.md#wait-for-a-required-business-milestone): after start success or matching-request deduplication, call `Client.waitForAttributeMatch` or `Client.waitForStepCompletion` for the committed condition or the specific persistence Step execution. Publish that condition only after the business effect commits. Bound the wait; timeout does not undo acceptance or prove the operation failed. An acceptance-only response needs no wait.

For an idempotent start, set one stable `StartFlowOptions.requestId` and `ignoreAlreadyStarted: true` only when a retry of that same logical request may attach to the existing run. A remaining `FlowAlreadyStartedError` means a different Request ID owns the Flow ID. Treat it as a domain conflict unless the coordinator contract deliberately redirects the command to that existing Flow. Another `DexServiceError` can leave acceptance unknown; retry with the same identities or read owning domain state, never a dedicated start-deduplication table.

For an explicitly best-effort external `client.writeStream`, log sanitized identity and discard only an `instanceof DexServiceError` failure. Rethrow other values so codec, definition, and programming defects remain visible.

[Pinned example error classification](https://github.com/superdurable/dex/blob/sdk-typescript/v1.30.0/examples/typescript/src/service-errors.ts)
<!-- dex-source: examples/typescript/src/service-errors.ts -->
```typescript
export function isFlowAlreadyStarted(error: unknown): boolean {
  return error instanceof FlowAlreadyStartedError;
}

export function isFlowMissingOrInactive(error: unknown): boolean {
  return (
    error instanceof FlowNotActiveOrNotFoundError ||
    error instanceof FlowNotFoundError
  );
}
```

The runnable examples classify with the exported concrete errors and treat `FlowNotActiveOrNotFoundError` and `FlowNotFoundError` as one missing-or-inactive outcome.

## Flow close cleanup

From TypeScript SDK v1.20.0 or later, narrow the caught value with `instanceof FlowClosingError` for a cleanup-sensitive Client call rejected with `ErrorSubStatus.FLOW_CLOSING` and FailedPrecondition. Wait for or re-read the retained terminal Flow state before deciding whether another action is needed. See the [affected calls and backend boundaries](../core/error-handling.md#flow-close-cleanup-rejection). [Released error](https://github.com/superdurable/dex/blob/sdk-typescript/v1.30.0/sdk-typescript/src/errors.ts)

## Indexed search limits

With server-zd `search.backend: postgres`, read the shared [indexed search limits](../core/error-handling.md#indexed-search-limits) first. At TypeScript SDK v1.30.0, a StartFlow or RPC rejected for an indexed Keyword value over 1,024 bytes rejects with `DexServiceError` whose `code` is `status.INVALID_ARGUMENT` and whose `subStatus` is `ErrorSubStatus.UNCATEGORIZED`; no subclass matches it. A Worker whose Attribute index registration exceeds capacity rejects `start()` with an `Error` whose `cause` is the gRPC FailedPrecondition error. The generated `ERROR_SUB_STATUS_ATTRIBUTE_INDEX_CAPACITY_EXCEEDED` and `ERROR_SUB_STATUS_INDEXED_KEYWORD_TOO_LONG` values exist only in the SDK's internal protocol bindings, which the package root does not export. A Flow that fails because a Step result carried such a value reports Flow error type `FlowErrorType.WORKER_API_FAILED` at TypeScript SDK v1.30.0. [Released Flow error type mapping](https://github.com/superdurable/dex/blob/sdk-typescript/v1.30.0/sdk-typescript/src/flow-result.ts) Validate indexed Keyword lengths before the write. [Released TypeScript error mapping](https://github.com/superdurable/dex/blob/sdk-typescript/v1.30.0/sdk-typescript/src/grpc-status.ts)

## Closed-Flow races

Follow the shared [closed-Flow race rule](../core/error-handling.md#closed-flow-races). `FlowNotActiveOrNotFoundError` from a mutation says it found no active target; it does not say the requested action succeeded. First reconcile from authoritative domain state and operation invariants. Call `await client.describeFlow(flowId)` only when an otherwise unknown terminal distinction changes the outcome. It reads the current or latest run without waiting. It resolves to `FlowInfo` with `flowId`, `runId`, `flowType`, `status`, and `startedAt`; it has no close time. Treat status `"completed"` as idempotent success only when successful completion guarantees the requested condition. For another terminal status or a `FlowNotFoundError`, return an explicit domain failure or unknown outcome. An incomplete server summary rejects with `TypeError`. If the Flow still appears running, query its domain state before a bounded, idempotent retry. [Released Client](https://github.com/superdurable/dex/blob/sdk-typescript/v1.30.0/sdk-typescript/src/client.ts) and [FlowInfo](https://github.com/superdurable/dex/blob/sdk-typescript/v1.30.0/sdk-typescript/src/options.ts)

## Query-only Get failures

Follow the shared [missing query target rule](../core/error-handling.md#missing-query-targets). For a business Get confirmed to have no registration or invocation locks, no transaction, no returned durable effects, and no Server policy that runs every RPC transactionally, catch `FlowNotActiveOrNotFoundError` or `FlowNotFoundError` with `instanceof` and return the contract's not-found result directly. Retained closed executions remain readable, so do not call WaitForFlow, describe/search/history APIs, or add a timeout probe or retry to distinguish missing from closed. Preserve other errors and any explicit retention/unavailable contract. Do not apply this translation to mutations or active-only RPC paths.

A business Get that reads with `client.getAttributes` translates `FlowNotFoundError` the same way. That call never rejects with `FlowNotActiveOrNotFoundError`, because a closed run stays readable until purge. Let `FlowDefinitionError`, `ValueMappingError`, and `TypeError` stay visible as local defects. See [Client Attribute reads](primitives.md#client-attribute-reads) for the full error list.

## Async discipline

Await every Client call. If a fire-and-forget call is intentional, attach rejection handling and document ownership. Let Express/Koa/Fastify error middleware translate expected application errors; do not erase `cause`, Flow ID, or method context.

## Recovery checklist

- Bound retries so recovery can run.
- Persist compensation facts before risky effects.
- Make each effect and compensation retry-safe.
- Log Flow ID, Run ID, StepExecution ID, method, attempt, and original cause.
- Treat cancellation as a request, not proof the remote effect stopped.
- Test failure before commit, after external success, during replacement, and after terminal Flow status.
