# TypeScript error handling

TypeScript rules for errors, retries, and recovery. Read
[core error handling](../core/error-handling.md) first for the boundary
rules. The blocks on this page form one module that type-checks;
`Shipment`, `ShipmentFlow`, `ProviderRateLimitedError`, `sendInvoiceEmail`,
and `invoiceEmailWasSent` are application declarations.

## Matching errors

- A thrown error or rejected Promise from `waitFor`, `execute`, an RPC, or
  `handleTimeout` fails that attempt. StepOptions decide retry and recovery.
- In a `catch` block keep the value `unknown` and narrow with `instanceof` on
  the exported classes. Never compare `error.message`. Keep `cause` when you
  wrap.

## Client errors

| Class | Meaning | Typical handling |
| --- | --- | --- |
| `FlowAlreadyStartedError` | The Flow ID is taken and the start did not attach | Domain conflict, unless the contract lets the existing Flow serve the request |
| `FlowNotFoundError` | No Flow with that ID is retained | Not-found result |
| `FlowNotActiveOrNotFoundError` | No active target for this call; missing or closed | Mutation: reconcile. Query-only Get: not-found result |
| `FlowClosingError` | Rejected during Flow close cleanup | Wait for or re-read the terminal state, then decide |
| `RpcLockConflictError` | An RPC requested a held lock | Bounded retry of an idempotent call |
| `WorkerInvocationError` | The Worker handler failed; see `workerErrorType`, `workerErrorDetail` | Surface or map; do not parse detail |
| `LongPollTimeoutError` | A long poll such as `waitForFlow` ended while the Flow is still open | Call again |
| `RequestTimeoutError` | A `requestTimeoutMs` budget expired | The wait ended; the Flow did not fail |
| `DexServiceError` | Base class of every remote error; has `code`, `subStatus`, `detail`, `operation`, `flowId` | Catch only where every remote outcome is handled the same |
| `FlowDefinitionError` | A Flow, RPC, or definition object is not registered or is invalid | Local defect |
| `ValueMappingError` | A codec cannot encode or decode a value | Local defect or bad data |
| `TypeError`, `RangeError` | Invalid arguments, thrown before any request | Local defect |
| `PullRegistrationError` | A Worker slot kind was rejected permanently | Startup or supervision failure |

Remote errors extend `DexServiceError`; local definition, codec, and argument
errors do not. `client.getAttributes` rejects with `FlowNotFoundError` for a
missing Flow, never `FlowNotActiveOrNotFoundError`, and with a plain `Error`
for an invalid response or a BlobCache failure.

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

- A retry that may attach to the same logical start keeps the same `flowId`,
  `requestId`, and `ignoreAlreadyStarted: true`.
- `requestId` alone still throws `FlowAlreadyStartedError`.
  `ignoreAlreadyStarted` alone does not attach to a run started with another
  request ID. An omitted `requestId` gets a fresh UUID on every call.
- For a promised milestone, follow a successful start with
  `waitForAttributeMatch` or `waitForStepCompletion` and a `requestTimeoutMs`.

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

Apply this translation only when the RPC registration has no
`lockAttributes`, no `isTransactional`, and returns no `nextSteps` or
`cancelingSteps`. A Get built on `client.getAttributes` translates
`FlowNotFoundError` the same way.

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
    try {
      const info = await client.describeFlow(flowId);
      return info.status === "completed" ? "already-done" : "unknown";
    } catch (describeError: unknown) {
      if (describeError instanceof FlowNotFoundError) {
        return "unknown";
      }
      throw describeError;
    }
  }
}
```

- Call `client.describeFlow` only when the terminal status changes the
  answer. `FlowInfo` has `flowId`, `runId`, `flowType`, `status`, and
  `startedAt`. Error details come from `waitForFlow` (`errorType`,
  `errorMessage`).
- Treat `status === "completed"` as success only when completion guarantees
  the requested condition.

## Best-effort Stream writes

For an explicitly best-effort `client.writeStream`, catch only
`DexServiceError`, log the Flow ID and `error.code`, and continue. Rethrow
everything else so codec and definition defects stay visible.

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

- Throw to let `executeRetry` own the retry.
- `throw retryAfter(seconds, cause)` only for a known delay, such as a
  provider rate-limit hint. `seconds` is a positive whole number and `cause`
  is an `Error`.

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

- A Step reached through `ExecuteFailure.proceedTo`,
  `FlowTimeoutHandlerFailure.proceedTo`, or `waitForFailure: "proceed"` reads
  `context.recoveryError` (`detail`, `errorType`, `attemptProgress`). Register
  it in `getSteps()`.
- Compare `attemptProgress` with the `StepAttemptProgress` constants. Only
  `NOT_STARTED` proves the handler never ran. Treat `NOT_COMPLETED`,
  `UNSPECIFIED`, and an omitted value as possibly run.
- Never branch on a fixed Dex `errorType` spelling for time limits.

## Async discipline

- Await every Client call and every `recordHeartbeat`. A deliberate
  fire-and-forget call gets a rejection handler and a named owner.
- Bound every Client wait: `timeoutMs` on `waitForFlow` and `readStream`,
  `requestTimeoutMs` on the match waits.
