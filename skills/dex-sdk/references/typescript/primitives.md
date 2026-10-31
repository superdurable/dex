# TypeScript primitives

Choose primitives from the business behavior first, then encode them with the SDK's TypeScript shapes.

| Primitive | TypeScript shape | Use it for | Key constraint |
| --- | --- | --- | --- |
| Flow | `Flow<I>` | Durable application boundary | Stable `getFlowType`, complete Step/schema registry |
| Step | `Step<I>` | Retryable work and transitions | Stable Step type and matching `Codec<I>` |
| Wait | `Wait.until`, `anyOf`, `allOf`, `anyCombinationOf` | Durable readiness | Wait is returned, never awaited as a Promise |
| Attribute / AttributeMap | `new Attribute`, `new AttributeMap` | Durable latest state | Durable name and codec are compatibility surface |
| Channel / ChannelMap | `new Channel`, `new ChannelMap` | Durable FIFO messages | Selected waits consume messages |
| RPC | `@rpc({...})` and `RPCResult<T>` | Synchronous command or query | Query-only handlers can read retained terminal runs; locks, transactions, and returned effects require active execution |
| Stream | `new Stream` | Best-effort progress | Not a source of truth |
| Timer | `Timer.byDuration`, `Timer.byTimestamp` | Durable deadlines | Milliseconds are numbers; no event-loop timer survives Worker loss |
| SubFlow | `SubFlow.run` | Durable child work | Parent/child lifetime is an application decision |
| Client | Promise-returning `Client` methods | Lifecycle, committed Attribute reads, RPCs, Attribute-match waits, Streams, search | Await network operations and catch typed errors |

## Step instances, run start time, and attempt deadline

From SDK v1.28.0, register several instances of one Step class by overriding `getStepType()` to return a distinct Step type for each; each instance keeps its own `inputCodec`, so a generic class can handle different input types in one Flow. `goTo`, `StepMovement.of`, `ExecuteFailure.proceedTo`, `FlowTimeoutHandlerFailure.proceedTo`, and cancellation selections accept a registered instance, `new StepRef("StepType")`, or a class. A class target is valid only when exactly one instance of that class is registered, and a new instance of a registered class is rejected because instances match by identity. `StepRef` holds only a Step type, cannot execute or appear in a `StepList`, and its input is checked by the destination codec at run time. Unknown or ambiguous targets raise `FlowDefinitionError` at registration or start and `InvalidStepResultError` for handler results.

The fields that hold targets (`StepMovement.step`, `ExecuteFailure.step`, `FlowTimeoutHandlerFailure.step`, and the cancellation fields) are typed `StepTarget` from v1.28.0. Code that reads one as a class must narrow first with `typeof target === "function"` or `target instanceof StepRef`. `Client.startFlow` now throws `FlowDefinitionError`, not `TypeError`, for a start-option failure target that does not belong to the Flow.

`Context.flowStartedAt` is the run's start time as a `Date`, identical across Steps and retries; derive deterministic identities from the Flow ID and this value rather than `Date.now()`. `Context.deadline` (v1.28.0) is the absolute `Date` deadline of the current `waitFor`, `execute`, or timeout-handler attempt, or `undefined` for RPC handlers and tasks without one. After checking for `undefined`, use `context.deadline.getTime() - Date.now()` for the remaining milliseconds and to size external call timeouts. A shorter remaining `RetryPolicy.totalDurationMs` shortens it, and each retry has its own. See the Core [attempt deadline guidance](../core/step-options.md#budget-work-against-the-attempt-deadline).

## Wait composition

[Pinned wait example](https://github.com/superdurable/dex/blob/sdk-typescript/v1.30.0/examples/typescript/src/primitives/wait-types/wait-types-flow.ts)
<!-- dex-source: examples/typescript/src/primitives/wait-types/wait-types-flow.ts -->
```typescript
if (input.mode === "any") {
  return Wait.anyOf(
    channelA.forOne("signal"),
    Timer.byDuration(timeoutMs, "timeout"),
  );
}
if (input.mode === "all") {
  return Wait.allOf(
    channelA.forOne("signal-a"),
    channelB.forOne("signal-b"),
  );
}
```

Among ready `Wait.anyOf` candidates, Dex uses canonical Timer, Channel, then SubFlow order and preserves argument order within each kind. An earlier unready Condition does not block a later ready one. Only the winning Channel consumes messages. For strict priority, return only the current higher-priority Condition until it resolves.

Use condition IDs when execution must distinguish winners and for every condition inside `anyCombinationOf`. Inspect results on the current `Context`.

## Codecs and schema

Use `stringCodec`, `booleanCodec`, `int64Codec`, `doubleCodec`, `bytesCodec`, or a deliberate `jsonCodec<T>` with validation for application models. Default JSON encoding does not validate TypeScript shapes at runtime. Register every Attribute, Channel, and Stream in exactly one Flow schema. For a short-lived secret, such as an API key, use a [hard-delete Attribute](data-handling.md#hard-delete-attributes); it supports only string and object values and cannot be indexed or synced.

## Stream reads

Use `Client.readStream` for forward, one-at-a-time, optionally long-polling consumption. Use `Client.listStreamMessages` for non-blocking newest-first pages. Pass the typed Stream directly, and pass `nextPageToken` unchanged until it is empty.

[Pinned runnable listing](https://github.com/superdurable/dex/blob/sdk-typescript/v1.30.0/examples/typescript/src/primitives/stream/controller.ts)
<!-- dex-source: examples/typescript/src/primitives/stream/controller.ts -->
```typescript
const page = await client.listStreamMessages(
  String(request.query.workflowId ?? ""),
  progress,
  Number(request.query.pageSize),
  String(request.query.beforePageToken ?? ""),
);
```

The before-page token is exclusive and scope-bound. The first page uses an empty token. Listing is a best-effort retained snapshot: concurrent newer writes stay outside the older-page chain, while trimming may remove messages. A trimmed anchor returns an empty page. The server requires a positive page size and caps it at 1000 by default.

## State, locks, and transaction

[Pinned Channel transaction example](https://github.com/superdurable/dex/blob/sdk-typescript/v1.30.0/examples/typescript/src/primitives/channel/channel-flow.ts)
<!-- dex-source: examples/typescript/src/primitives/channel/channel-flow.ts -->
```typescript
@rpc({
  isTransactional: true,
  loadChannels: [queuedMessages],
  inputCodec: queuedMessageReferenceCodec,
})
public moveQueuedMessageToPrioritizedMessages(
  context: Context,
  queuedMessage: QueuedMessageReference,
): void {
  const messageToPrioritize = queuedMessages.findPendingMessage(
    context,
    queuedMessage.messageId,
  );
  queuedMessages.delete(context, queuedMessage.messageId);
  if (messageToPrioritize !== undefined) {
    prioritizedMessages.publish(context, messageToPrioritize.value);
  }
}
```

Staged Attribute and Channel mutations commit with a successful handler result. An exception discards that attempt's mutations. Locks coordinate only handlers requesting the same lock; external effects still need idempotency.

Pending-message reads inside a Step or RPC are invocation snapshots. Other handlers may consume, delete, or publish concurrently. Transactional execution validates selected deletions and commits writes atomically, but does not lock the whole snapshot. Read and write pending messages directly only when the operation explicitly tolerates that race. When a decision requires the queue to remain unchanged, every cooperating Step and RPC writer must use the same Attribute lock.

Read committed Attributes from outside the Flow with [`client.getAttributes`](#client-attribute-reads). Use typed RPCs for application writes of Flow-owned Attribute and Channel state, and for reads that need Flow code or pending Channel messages. The Client has no direct write method. Attribute match remains the blocking observation surface.

An RPC without Attribute locks or `isTransactional` can query a retained terminal run when it returns no durable effects. Locks, transactions, returned effects, or Server policy require an active execution. RPC success alone does not prove the Flow is active.

## Client Attribute reads

From TypeScript SDK v1.29.0, `client.getAttributes(flowId, ...reads)` reads committed values of registered Attributes and AttributeMap instances in one FlowService request, without a Worker. Follow the shared [semantics and selection rule](../core/primitives.md#read-attributes-from-outside-a-flow). Pass singleton Attributes, or AttributeMap instances selected with `attributeMap.instance(key)`. There is no single-Attribute method; pass one read to read one value.

[Released TypeScript Client Attribute read](https://github.com/superdurable/dex/blob/sdk-typescript/v1.30.0/sdk-typescript/README.md)
<!-- dex-source: sdk-typescript/README.md -->
```typescript
const [status, note] = await client.getAttributes(
  "order-42",
  orderStatus,
  notes.instance("n1"),
);
```

The call resolves to a typed tuple in read order. Each element is `T | undefined`, and `undefined` means the value is not set. Writing `null` or `undefined` deletes an Attribute, so it then reads as `undefined`. Falsy values such as `""`, `0n`, and `false` are set values. A stored value that its codec decodes to `null` stays `null`. Zero reads do not compile; to spread an array, pass its first read separately, as in `client.getAttributes(flowId, first, ...rest)`. `AttributeMapInstance` is nominally branded, so `notes.load("n1")` and object literals are rejected at compile time.

Each definition must be the exact object that a Flow in the Client's Registry returns from `getPersistenceSchema`. A different object with the same name, such as one from a second `hardDeleteOnNull()` call, is rejected. Errors:

- `FlowNotFoundError`: the Flow does not exist, or its latest run is being purged. A closed run is still readable. At a business Get boundary, return the not-found result directly.
- `FlowDefinitionError`: a definition is not the registered object. It is thrown before any request.
- `TypeError`: an empty Flow ID, no reads, a read that is not an Attribute or AttributeMap instance selection, or a duplicate read. It is thrown before any request.
- `ValueMappingError`: a value cannot be hydrated or decoded with its codec.
- `DexServiceError`: another FlowService failure. A failed LoadBlobs request has `operation` `loadBlobs` and keeps the gRPC `code`.
- `Error`: an invalid server response, or a BlobCache failure while large values are hydrated.

On any error the Promise rejects and returns no values. [Released Client API](https://github.com/superdurable/dex/blob/sdk-typescript/v1.30.0/sdk-typescript/src/client.ts)

## Decisions

Use `await client.describeFlow(flowId)` for a one-off status read of a known Flow, such as after a read-only RPC returns non-terminal business state. `FlowInfo` contains Flow ID, run ID, Flow type, status, and start time. A missing Flow throws `FlowNotFoundError`. The call does not wait for terminal status: use `waitForFlow` when the caller must wait, and do not loop on `describeFlow`. `FlowInfo` has no error type or message; for a closed Flow that is still retained, `waitForFlow` returns immediately, and a failed result carries them in `errorType` and `errorMessage`. Do not use `describeFlow` as a `startFlow` preflight. Use `searchFlows` for indexed discovery. [Released TypeScript Client API](https://github.com/superdurable/dex/blob/sdk-typescript/v1.30.0/sdk-typescript/src/client.ts)

Use `goTo`, `goToMany`, `gracefulComplete`, `forceComplete`, `forceFail`, `deadEnd`, and conditional completion helpers to express lifecycle. Do not encode control flow in mutable module globals. A Flow can have `StepList.empty()` or `withoutStartStep` when started work is driven by RPCs.
