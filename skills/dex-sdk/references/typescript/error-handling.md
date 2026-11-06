# TypeScript error handling

TypeScript rules for errors, retries, and recovery. Read
[core error handling](../core/error-handling.md) first for the boundary rules:
start first, reconcile only after an error, closed-Flow races, retry
ownership, and attempt progress.

## Error layers

Keep three layers apart:

1. Handler failures. A thrown error or rejected Promise from `waitFor`,
   `execute`, an RPC, or `handleTimeout` fails that attempt. StepOptions
   decide the retry and the recovery route.
2. Typed Client errors. They describe a Dex outcome for one Client call.
3. Caller and transport conditions: aborts, network failures, a request
   budget that expired.

In a `catch` block keep the value `unknown` and narrow with `instanceof`.
Never compare `error.message` text. Keep `cause`, the Flow ID, and the
operation when you wrap.

## Client errors

| Class | Meaning | Typical handling |
| --- | --- | --- |
| `FlowAlreadyStartedError` | The Flow ID is taken and the start did not attach | Domain conflict, unless the contract lets the existing Flow serve the request |
| `FlowNotFoundError` | No Flow with that ID is retained | Not-found result |
| `FlowNotActiveOrNotFoundError` | No active target for this call; missing or closed | Mutation: reconcile. Query-only Get: not-found result |
| `FlowClosingError` | Rejected during Flow close cleanup | Wait for or re-read the terminal state, then decide |
| `RpcLockConflictError` | An RPC requested a held lock | Contention; bounded retry of an idempotent call |
| `WorkerInvocationError` | The Worker handler failed; see `workerErrorType`, `workerErrorDetail` | Surface or map; do not parse detail |
| `LongPollTimeoutError` | A long poll such as `waitForFlow` ended while the Flow is still open | Call again; the Flow did not fail |
| `RequestTimeoutError` | A `requestTimeoutMs` budget expired | The wait ended; the Flow and condition did not fail |
| `DexServiceError` | Base class of every remote error; has `code`, `subStatus`, `detail`, `operation`, `flowId` | Catch only at a boundary that treats every remote outcome the same |
| `FlowDefinitionError` | A Flow, RPC, or definition object is not registered or is invalid | Local defect; fix the code |
| `ValueMappingError` | A value cannot be encoded or decoded by its codec | Local defect or bad data |
| `TypeError`, `RangeError` | Invalid arguments, thrown before any request | Local defect |
| `PullRegistrationError` | A Worker slot kind was rejected permanently | Startup or supervision failure |

Concrete remote errors extend `DexServiceError`. Local definition, codec,
and argument errors do not. `client.getAttributes` additionally rejects with
`FlowNotFoundError` for a missing Flow (never `FlowNotActiveOrNotFoundError`,
because a closed run stays readable) and with a plain `Error` for an invalid
response or a BlobCache failure.

## StartFlow

```typescript
export async function startShipment(client: Client, flow: ShipmentFlow, request: Shipment): Promise<"accepted" | "conflict"> {
  try {
    await client.startFlow(flow, `shipment-${request.shipmentId}`, request, {
      requestId: `start-shipment-${request.shipmentId}`,
      ignoreAlreadyStarted: true,
      idReusePolicy: IdReusePolicy.DISALLOW,
    });
    return "accepted";
  } catch (error: unknown) {
    if (error instanceof FlowAlreadyStartedError) {
      return "conflict";
    }
    throw error;
  }
}
```

- Call `startFlow` directly. Do not preflight with an RPC, `describeFlow`, or
  search.
- A retry that may attach to the same logical start keeps the same `flowId`,
  the same `requestId`, and `ignoreAlreadyStarted: true`. Dex compares only
  the request ID, so bind it to the complete immutable request.
- `requestId` alone still throws `FlowAlreadyStartedError`.
  `ignoreAlreadyStarted` alone does not suppress a conflict with another
  request ID. An omitted `requestId` gets a fresh UUID on every call.
- A remaining `FlowAlreadyStartedError` means another request owns the Flow ID.
  Only in that branch may you call a typed read RPC to decide whether the
  existing Flow serves this request.
- Another `DexServiceError` can leave acceptance unknown. Retry with the same
  identities and options, or read owning domain state. Never add a separate
  start-deduplication table.
- When the API promises that work finished, wait after a successful start
  with `waitForAttributeMatch` or `waitForStepCompletion` and a
  `requestTimeoutMs`. A timeout does not undo the start.
- An API handler does not start dependent Flows itself. A Step starts them
  through an injected Client with stable downstream identities.

## Query-only Get

```typescript
export async function getShipmentStatus(client: Client, flow: ShipmentFlow, flowId: string): Promise<string | undefined> {
  try {
    return await client.invokeRPC(flow.getShipmentStatus, flowId);
  } catch (error: unknown) {
    if (error instanceof FlowNotActiveOrNotFoundError || error instanceof FlowNotFoundError) {
      return undefined;
    }
    throw error;
  }
}
```

Translate to not-found only when the RPC registration has no locks, no
`isTransactional`, and returns no durable effects. A retained closed run stays
readable, so add no lifecycle probe, history read, or retry. A Get built on
`client.getAttributes` translates `FlowNotFoundError` the same way.

## Closed-Flow races

```typescript
export async function cancelShipment(client: Client, flow: ShipmentFlow, flowId: string): Promise<"canceled" | "already-done" | "unknown"> {
  try {
    const isCanceled = await client.invokeRPC(flow.cancelShipment, flowId, "customer request");
    return isCanceled ? "canceled" : "already-done";
  } catch (error: unknown) {
    if (!(error instanceof FlowNotActiveOrNotFoundError)) {
      throw error;
    }
    const info = await client.describeFlow(flowId);
    return info.status === "completed" ? "already-done" : "unknown";
  }
}
```

- `FlowNotActiveOrNotFoundError` from a mutation means no active target. It
  does not mean the action happened.
- Reconcile from domain state first. Call `client.describeFlow` only when the
  terminal status changes the answer. Treat `"completed"` as success only when
  completion guarantees the requested condition.
- `FlowInfo` has `flowId`, `runId`, `flowType`, `status`, and `startedAt`. It
  has no error detail; `waitForFlow` on a closed Flow returns `errorType` and
  `errorMessage`.
- `FlowClosingError` means the Flow is closing. Wait for or re-read the
  terminal state before another action.

## Best-effort Stream writes

For an explicitly best-effort `client.writeStream`, catch only
`DexServiceError`, log the Flow ID and `error.code`, and continue. Rethrow
everything else so codec and definition defects stay visible. A disabled or
full Stream Store also surfaces as `DexServiceError`; see
[core error handling](../core/error-handling.md) for which status to retry.
A Stream write inside a Step does not fail the handler on storage failure.

## Handler retries

```typescript
class SendInvoice implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "SendInvoice";
  }

  public async execute(context: Context, invoiceId: string): Promise<StepDecision> {
    try {
      await sendInvoiceEmail(invoiceId, `${context.flowId}:${invoiceId}`);
    } catch (error: unknown) {
      if (error instanceof ProviderRateLimitedError) {
        throw retryAfter(error.retryAfterSeconds, error);
      }
      throw error;
    }
    return gracefulComplete(invoiceId);
  }
}
```

- Throw to let Step retry own the retry. Never wrap Step work in an in-memory
  retry loop.
- `throw retryAfter(seconds, cause)` only when the application knows a
  meaningful delay, such as a provider rate-limit hint. "Not ready yet" is a
  polling branch, never an error ([patterns](patterns.md#polling)).
- Do not catch and return a success decision just to stop retries. That
  commits staged writes.
- Derive external idempotency keys from durable identity (Flow ID plus a
  persisted business key). After a side effect, persist the fact or move to a
  recovery-capable Step.

## Recovery

```typescript
class RecoverInvoice implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "RecoverInvoice";
  }

  public async execute(context: Context, invoiceId: string): Promise<StepDecision> {
    const failure = context.recoveryError;
    const progress = failure?.attemptProgress ?? StepAttemptProgress.UNSPECIFIED;
    if (progress === StepAttemptProgress.NOT_STARTED) {
      await sendInvoiceEmail(invoiceId, `${context.flowId}:${invoiceId}`);
      return gracefulComplete(invoiceId);
    }
    const wasSent = await invoiceEmailWasSent(`${context.flowId}:${invoiceId}`);
    return gracefulComplete(wasSent ? invoiceId : `failed: ${failure?.detail ?? "unknown"}`);
  }
}
```

- A recovery Step reached through `ExecuteFailure.proceedTo`,
  `FlowTimeoutHandlerFailure.proceedTo`, or `waitForFailure: "proceed"` reads
  `context.recoveryError` (`detail`, `errorType`, `attemptProgress`). Register
  it in `getSteps()`.
- Compare `attemptProgress` with the `StepAttemptProgress` constants, not
  string literals. Only `NOT_STARTED` proves the handler never ran; only SYNC
  Steps report it. Treat `NOT_COMPLETED`, `UNSPECIFIED`, and an omitted value
  as possibly run, and reconcile external effects before repeating them.
- Never branch on a fixed Dex time-limit `errorType` spelling. Branch on error
  types the application defines.
- Compensation is idempotent and carries the identity of the original
  commit.

## Async discipline

- Await every Client call and every `recordHeartbeat`. If a call is
  intentionally fire-and-forget, attach a rejection handler and document who
  owns the failure.
- Give every Client wait a bound: `timeoutMs` on `waitForFlow` and
  `readStream`, `requestTimeoutMs` on the match waits.
- Let HTTP framework error middleware map expected application errors. Do not
  erase `cause`.
