# TypeScript primitives

TypeScript shapes for each Primitive. Read the matching core file first for
the concept and its guarantees. Every skeleton type-checks against the
current SDK. Imports come from `@superdurable/dex`.

| Primitive | TypeScript shape | Core |
| --- | --- | --- |
| Flow | class `implements Flow<I>` | [flow](../core/flow.md) |
| Step | class `implements Step<I>`; decisions `goTo`, `goToMany`, `gracefulComplete`, `forceComplete`, `forceFail`, `deadEnd` | [step](../core/step.md) |
| Wait | `Wait.until`, `allOf`, `anyOf`, `anyCombinationOf`, `skipImmediately` | [step](../core/step.md) |
| Timer | `Timer.byDuration(ms, conditionId?)` in `waitFor` | [timer](../core/timer.md) |
| Attribute | `new Attribute(name, codec, index?)`, `new AttributeMap(...)` | [attribute](../core/attribute.md) |
| Channel | `new Channel(name, codec)`, `new ChannelMap(...)` | [channel](../core/channel.md) |
| RPC | `@rpc({...})` or `declareRPCMethod(...)`, returns `RPCResult<T>` or `void` | [rpc](../core/rpc.md) |
| Stream | `new Stream(name, codec, capacityBytes)` | [stream](../core/stream.md) |
| SubFlow | `SubFlow.run(flow, input, options?)` in `waitFor` | [subflow](../core/subflow.md) |
| Client | `Client` methods return Promises | [client](../core/client.md) |

## Flow and Step

```typescript
interface Order {
  readonly orderId: string;
  readonly amountCents: number;
}

const orderCodec = jsonCodec<Order>({
  typeName: "Order",
  decode: (value) => {
    const candidate = value as Partial<Order>;
    if (typeof candidate.orderId !== "string" || typeof candidate.amountCents !== "number") {
      throw new TypeError("invalid Order");
    }
    return { orderId: candidate.orderId, amountCents: candidate.amountCents };
  },
});

const orderStatus = new Attribute("order-status", stringCodec, {
  type: IndexType.KEYWORD,
  indexKey: "keyword1",
});
const approvals = new Channel("approvals", stringCodec);

class ValidateOrder implements Step<Order> {
  public readonly inputCodec = orderCodec;

  public getStepType(): string {
    return "ValidateOrder";
  }

  public execute(context: Context, order: Order): StepDecision {
    orderStatus.set(context, "validated");
    return goTo(AwaitApproval, order);
  }
}

export class OrderFlow implements Flow<Order> {
  private readonly validateOrder = new ValidateOrder();
  private readonly awaitApproval = new AwaitApproval();
  private readonly chargeOrder = new ChargeOrder();
  private readonly recordChargeFailure = new RecordChargeFailure();

  public getFlowType(): string {
    return "OrderFlow";
  }

  public getSteps(): StepList<Order> {
    return StepList.startStep(this.validateOrder).otherSteps(
      this.awaitApproval,
      this.chargeOrder,
      this.recordChargeFailure,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [orderStatus], channels: [approvals] };
  }
}
```

Rules:

- `StepList.startStep(step).otherSteps(...)` registers the start Step and every
  other reachable Step. A Flow driven only by RPCs uses
  `StepList.withoutStartStep(...)` or `StepList.empty()`. The start Step input
  type must equal the Flow's `I`.
- A Step without `waitFor` executes immediately.
- A movement target (`goTo`, `StepMovement.of`, failure routes, cancellation)
  may be the Step class, the registered instance, or `new StepRef("StepType")`.
  A class target needs exactly one registered instance of that class. Use
  `StepRef` for a Step defined later or in another module; its input is
  checked only by the destination codec at run time.
- Several instances of one Step class may be registered when each returns a
  distinct `getStepType()`. Target them by instance or `StepRef`.
- `gracefulComplete` waits for other active branches. `forceComplete` and
  `forceFail` close the Flow now. `deadEnd` ends only this branch.
  `forceCompleteIfChannelsEmpty(output, fallbackMovement, ...channels)`
  completes atomically only when the named Channels are empty.
- Derive deterministic identities from `context.flowId` and
  `context.flowStartedAt`, never from `Date.now()` or random values.
- `context.deadline` is the absolute deadline of the current `waitFor`,
  `execute`, or timeout-handler attempt, or `undefined` (RPC handlers). Size
  external call timeouts from `context.deadline.getTime() - Date.now()`.
  Never store a deadline in a heartbeat checkpoint.
- `context.attempt` is the retry attempt of this method call.
  `context.recoveryError` is set on a recovery path
  ([error handling](error-handling.md#recovery)).

## Wait and Timer

```typescript
class AwaitApproval implements Step<Order> {
  public readonly inputCodec = orderCodec;

  public getStepType(): string {
    return "AwaitApproval";
  }

  public waitFor(_context: Context, _order: Order): Wait {
    return Wait.anyOf(approvals.forOne(), Timer.byDuration(24 * 60 * 60_000, "approval-deadline"));
  }

  public execute(context: Context, order: Order): StepDecision {
    if (context.hasTimerFired()) {
      orderStatus.set(context, "expired");
      return gracefulComplete("expired");
    }
    const [decision] = approvals.results(context);
    orderStatus.set(context, decision ?? "unknown");
    return goTo(new StepRef("ChargeOrder"), order);
  }
}
```

- `waitFor` returns a `Wait`. It has no side effects on external systems.
- Among ready `anyOf` conditions Dex picks Timers, then Channels, then
  SubFlows, in argument order within a kind. Only the winning Channel
  condition consumes messages. For strict priority, return only the
  higher-priority condition until it resolves.
- In `execute`, read the winner with `context.hasTimerFired(index?)`,
  `channel.results(context)`, or `SubFlow.getConditionResults(context, i)`.
- `anyCombinationOf(ConditionCombination.of(...))` requires a non-empty,
  unique `conditionId` on every condition.
- A Timer `conditionId` lets an operator skip it with `client.skipTimer`.
- `setTimeout` or `sleep` in a handler is not a durable Timer. Put business
  time (deadlines, reminders, inactivity) in `waitFor`.

## StepOptions, retry, and durability

```typescript
class ChargeOrder implements Step<Order> {
  public readonly inputCodec = orderCodec;

  public getStepType(): string {
    return "ChargeOrder";
  }

  public getStepOptions(): StepOptions {
    return {
      executeMethodTimeoutMs: 60_000,
      executeRetry: { maximumAttempts: 5 },
      executeDurability: "sync",
      executeFailure: ExecuteFailure.proceedTo(RecordChargeFailure),
    };
  }

  public async execute(context: AsyncContext, order: Order): Promise<StepDecision> {
    const idempotencyKey = `${context.flowId}:charge`;
    await chargeCard(order, idempotencyKey, context.cancellationSignal);
    orderStatus.set(context, "charged");
    return gracefulComplete(order.orderId);
  }
}
```

- `getStepOptions()` holds method timeouts, `heartbeatTimeoutMs`,
  `waitForRetry`/`executeRetry` (`RetryPolicy`), durability, locks, selective
  loads, `waitForFailure: "proceed"`, and `executeFailure`.
- `ExecuteFailure.proceedTo(Target)` routes exhausted retries to a registered
  recovery Step. It receives the failing Step's input. Without it the Flow
  fails.
- Durability is `"sync"` or `"async"`. Set a Flow-wide default with
  `StartFlowOptions.configOverride.stepDurability`, then override per method
  with `waitForDurability` or `executeDurability`. Use `"async"` only for
  idempotent work ([step](../core/step.md)).
- Do not infer durability from a timeout. Defaults and units are in the
  installed TSDoc.

## Heartbeat and cancellation

- Take `AsyncContext` and `await context.recordHeartbeat(value, codec?)`
  within the heartbeat timeout. Read the last checkpoint with
  `context.getLastHeartbeatValue(codec?)`. Use the same codec for both.
  `context.hasLastHeartbeatValue()` tells absent from a stored `null`.
- A Stream write also counts as liveness, but it does not replace a checkpoint.
- Check `context.cancellationSignal.aborted` between work units and pass the
  signal to `fetch` and other abort-aware calls. Return promptly when it
  aborts.
- `context.setStepExecutionLocal` passes process-local data from `waitFor` to
  `execute`. It is lost on Worker replacement. Treat absence as normal.

## Attribute and AttributeMap

```typescript
interface Note {
  readonly text: string;
}

const notes = new AttributeMap("notes", jsonCodec<Note>());
const tenantCommands = new ChannelMap("tenant-commands", stringCodec);
const apiKey = new Attribute("api-key", stringCodec).hardDeleteOnNull();
const customerEmail = new Attribute("customer-email", stringCodec).syncToAttributeStore();
```

- Define every Attribute, AttributeMap, Channel, ChannelMap, and Stream once at
  module scope or as a Flow field. Register it in exactly one Flow's
  `getPersistenceSchema()`. Reuse the same object in handlers and Client calls.
- Handler access: `attribute.get/set/delete(context)`;
  `attributeMap.get/set/delete(context, instance)`,
  `getAllInstanceKeys(context)`, `getMapSize(context)`. Setting `null` or
  `undefined` deletes.
- Singleton Attributes load automatically. AttributeMap instances load only
  when selected: `loadAttributeMaps` (whole map) or
  `loadAttributeMapInstances: [map.load(instance)]` in StepOptions or
  RPCOptions. Reading an unselected instance throws
  `AttributeMapNotLoadedError`. That is modeling feedback, not absence.
- Locks: `attribute.lock()` or `attributeMap.lock(instance)` in
  `waitForLockAttributes`, `executeLockAttributes`, or RPC `lockAttributes`.
  A lock coordinates only handlers that request the same lock. Lock the
  smallest stable key. Keep remote I/O out of locked handlers.
  Read-modify-write of a map instance selects both its lock and its load.
- Index for search: pass `{ type: IndexType.KEYWORD, indexKey: "keyword1" }`.
  Keep the business Attribute name and use generic typed index keys
  (`keyword1`, `text1`, `int1`). Reuse the same slots across Flow types and
  filter searches by Flow type.
- `syncToAttributeStore()` projects latest values asynchronously to the
  Attribute Stores named in `FlowConfig.attributeStoreNames`. The Flow stays
  authoritative. Never wait on the projection for correctness.
- `hardDeleteOnNull()` is for short-lived secrets. It accepts only string and
  object values and cannot be indexed or synced. `delete` removes the value
  permanently. Pass it at start with `InitialAttribute.of(apiKey, value)` in
  `StartFlowOptions.attributes`. `waitForAttributeMatch` rejects it. Writes
  can become visible before the attempt commits, so keep handlers repeatable
  and never read-modify-write the secret.

## Channel and ChannelMap

- Publish with `channel.publish(context, value)` or
  `channelMap.publish(context, instance, value)` from a Step or an RPC.
- Wait with `forOne()`, `forN(n)`, `atLeast`, `atMost`, `range`. The satisfied
  condition consumes its messages. Read them with `channel.results(context)`.
- Pending messages load only when selected (`loadChannels`,
  `loadChannelMapInstances: [map.loadMessages(instance)]`). An RPC can read
  `pendingMessages`, `findPendingMessage`, and `delete(context, messageId)`.
- A pending-message read is a snapshot. Other handlers may change the queue.
  `isTransactional: true` validates selected deletions and commits atomically,
  but does not lock the snapshot. When a decision needs the queue unchanged,
  every cooperating writer uses the same Attribute lock.
- External callers publish through a typed RPC. The Client has no direct
  Channel write.

## RPC

```typescript
  @rpc({ inputCodec: stringCodec })
  public submitApproval(context: Context, decision: string): void {
    approvals.publish(context, decision);
  }

  @rpc({ outputCodec: stringCodec })
  public getOrderStatus(context: Context): RPCResult<string> {
    return { output: orderStatus.get(context) };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: booleanCodec, lockAttributes: [orderStatus.lock()] })
  public cancelOrder(context: Context, reason: string): RPCResult<boolean> {
    if (orderStatus.get(context) === "charged") {
      return { output: false };
    }
    orderStatus.set(context, `canceled: ${reason}`);
    return { output: true, cancelingSteps: [AwaitApproval] };
  }
```

Decorator-free form, for runtimes that cannot transform decorators:

```typescript
declareRPCMethod(NotebookFlow, "updateNote", { outputCodec: booleanCodec });
```

- An RPC is a public Flow method whose first parameter is `Context`. It
  returns `RPCResult<T>`, a Promise of it, or `void` for a procedure.
- Name RPCs with an action verb: `getOrderStatus`, `submitApproval`. The
  method name is the RPC name. `RPCOptions.name` overrides it, for example to
  serve an RPC that another language's Flow defines. Keep RPC names stable.
- `declareRPCMethod(FlowClass, "method", options)` takes the same options.
  Call it at module top level after the class and before building the
  Registry. Declare each method once, on the class that defines it. Arrow
  function fields cannot be RPCs. One Flow may mix both forms.
- Registration-time options fix codecs, `timeoutMs`, `lockAttributes`,
  `isTransactional`, and loads. Per call, `client.invokeRPCWithOptions(...)`
  adds only `lockAttributeMapInstances`, `loadAttributeMapInstances`, and
  `loadChannelMapInstances`.
- `RPCResult.nextSteps` schedules Steps and `cancelingSteps` cancels every
  current execution of the named Step types, atomically with the RPC's writes.
- An RPC without locks, without `isTransactional`, and without returned
  effects can read a retained closed run. Anything else needs an active run.
- Keep handlers short. An RPC that requests a held lock fails with
  `RpcLockConflictError`.

```typescript
const existed = await client.invokeRPCWithOptions(
  notebookFlow.updateNote,
  flowId,
  { noteId: "n1", text: "hello" },
  {
    lockAttributeMapInstances: [notes.lock("n1")],
    loadAttributeMapInstances: [notes.load("n1")],
  },
);
```

## Stream

```typescript
const progress = new Stream("progress", stringCodec, 1 << 20);

class RenderReport implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "RenderReport";
  }

  public async execute(context: AsyncContext, reportId: string): Promise<StepDecision> {
    progress.write(context, `started ${reportId}`);
    const writer = progress.bufferedText(context);
    writer.write("chunk-1");
    writer.write("chunk-2");
    return gracefulComplete(reportId);
  }
}
```

- Register the Stream in `getPersistenceSchema().streams`. A Stream is a best
  effort feed. Retries can repeat messages, and trimming can drop them.
- `stream.write` sends immediately and does not fail the handler on storage
  failure. `bufferedText` (string Streams in a Step) batches chunks and
  flushes before the result.
- Consumers: `client.readStream(flowId, stream, resumeToken, timeoutMs?)`
  reads forward one message and returns its `resumeToken`. Persist and reuse
  it. `client.listStreamMessages(flowId, stream, pageSize, beforePageToken)`
  returns newest-first pages. Pass `nextPageToken` back unchanged.
- `client.writeStream(flowId, stream, source, value)` writes from outside a
  Flow ([error handling](error-handling.md#best-effort-stream-writes)).

## SubFlow

```typescript
class RunChildren implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly childFlow: ChildFlow) {}

  public getStepType(): string {
    return "RunChildren";
  }

  public waitFor(_context: Context, count: number): Wait {
    return Wait.allOf(
      ...Array.from({ length: count }, (_, index) => SubFlow.run(this.childFlow, index)),
    );
  }

  public execute(context: Context, count: number): StepDecision {
    let total = 0;
    for (let index = 0; index < count; index++) {
      const result = SubFlow.getConditionResults(context, index);
      if (result.status !== "completed") {
        return gracefulComplete(`child ${SubFlow.getFlowId(context, index)} ended ${result.status}`);
      }
      total += result.singleOutput(doubleCodec);
    }
    return gracefulComplete(total);
  }
}
```

- Register both parent and child Flow instances in the same Registry. Pass
  the registered child instance to `SubFlow.run`.
- A failed child is a result to inspect, not an exception. Check
  `result.status` before decoding output.
- With `anyOf`, losing children keep running. Decide explicitly what happens
  to them.

## Client

```typescript
const runId = await client.startFlow(orderFlow, "order-42", { orderId: "42", amountCents: 100 }, {
  requestId: "start-order-42",
  ignoreAlreadyStarted: true,
});
await client.invokeRPC(orderFlow.submitApproval, "order-42", "approved");
const matched = await client.waitForAttributeMatch(
  "order-42",
  orderStatus,
  AttributeMatch.equalTo("charged"),
  { requestTimeoutMs: 30_000 },
);
await client.waitForStepCompletion("order-42", StepExecutionId.of("ChargeOrder"), {
  requestTimeoutMs: 30_000,
});
const info = await client.describeFlow("order-42");
```

- `startFlow(flow, flowId, input, options)` returns the run ID after Dex
  accepts the start. It does not mean any Step ran.
- `invokeRPC(flow.method, flowId, input?)` takes the bound method of the
  registered Flow instance.
- `waitForFlow(flowId, timeoutMs?)` returns a `FlowResult` for a closed Flow.
  It throws `LongPollTimeoutError` while the Flow is still open; call again.
  Check `result.status` before `singleOutput(codec)`.
- `waitForAttributeMatch` (singleton or map instance) and
  `waitForStepCompletion` wait for a committed milestone. `requestTimeoutMs`
  bounds the whole call; omit it only for a deliberate unbounded wait. After
  the wait, read canonical state again.
- `describeFlow(flowId)` is a one-off status read of a known Flow. Do not loop
  on it and do not use it as a start preflight.
- `searchFlows(query, pageSize, nextPageToken)` discovers Flows by indexed
  Attributes.
- `stopFlow`, `timeTravel`, `skipTimer`, and `updateFlowConfig` are
  operator actions. Use them deliberately.
- The Client has no direct Attribute or Channel write. Write through typed
  RPCs.

## Client Attribute reads

```typescript
const [status, note] = await client.getAttributes(
  "order-42",
  orderStatus,
  notes.instance("n1"),
);
```

- `client.getAttributes(flowId, ...reads)` reads committed values of
  singleton Attributes and AttributeMap instances in one call, without a
  Worker. It also reads a retained closed run.
- Pass singleton Attributes, or map instances selected with
  `map.instance(key)`. `map.load(key)` is a load selection and does not
  compile here. Pass at least one read.
- The result is a typed tuple in read order. `undefined` means not set.
  Falsy values such as `""`, `0n`, and `false` are set values.
- Each read must be the exact definition object a registered Flow returns
  from `getPersistenceSchema()`. Another object with the same name fails with
  `FlowDefinitionError` before any request.
- Use `getAttributes` for a plain read of stored state. Use a read RPC when
  Flow code must shape the answer or read pending Channel messages.
- Errors are listed in [error handling](error-handling.md#client-errors).

Runnable primitives:
[examples/typescript/src/primitives](https://github.com/superdurable/dex/tree/main/examples/typescript/src/primitives),
[decorator-free RPC](https://github.com/superdurable/dex/tree/main/examples/typescript/decorator-free-rpc).
