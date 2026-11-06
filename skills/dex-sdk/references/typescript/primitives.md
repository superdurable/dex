# TypeScript primitives

TypeScript shapes for each Primitive. Read the matching core file first for
the concept and its guarantees. The blocks on this page form one module that
type-checks against the current SDK. Every name comes from
`@superdurable/dex`, except `chargeCard`, an application function.

Core files: [flow](../core/flow.md), [step](../core/step.md),
[timer](../core/timer.md), [attribute](../core/attribute.md),
[channel](../core/channel.md), [rpc](../core/rpc.md),
[stream](../core/stream.md), [subflow](../core/subflow.md),
[client](../core/client.md).

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
}
```

- `StepList.startStep(step).otherSteps(...)` registers the start Step and every
  other reachable Step. A Flow driven only by RPCs uses
  `StepList.withoutStartStep(...)` or `StepList.empty()`. The start Step input
  type equals the Flow's `I`.
- A Step without `waitFor` executes immediately.
- A movement target (`goTo`, `StepMovement.of`, failure routes, cancellation)
  is the Step class, the registered instance, or `new StepRef("StepType")`. A
  class target needs exactly one registered instance of that class. Use
  `StepRef` for a Step defined later or in another module; only the
  destination codec checks its input, at run time.
- Several instances of one Step class may be registered when each returns a
  distinct `getStepType()`. Target them by instance or `StepRef`.
- `forceCompleteIfChannelsEmpty(output, fallbackMovement, ...channels)`
  completes only when the named Channels are empty, otherwise it schedules the
  fallback.
- Derive deterministic identities from `context.flowId` and
  `context.flowStartedAt`, never from `Date.now()` or random values.
- `context.deadline` is the absolute deadline of the current `waitFor`,
  `execute`, or timeout-handler attempt, or `undefined` in RPC handlers. Size
  external call timeouts from `context.deadline.getTime() - Date.now()`.
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

- `waitFor` returns a `Wait`. It never awaits or sleeps.
- In `execute`, find the `anyOf` winner with `context.hasTimerFired(index?)`,
  `channel.results(context)`, or `SubFlow.getConditionResults(context, i)`.
- `Wait.anyCombinationOf(ConditionCombination.of(...))` requires a non-empty,
  unique `conditionId` on every condition.
- A Timer `conditionId` lets an operator skip it with `client.skipTimer`.
- `setTimeout` or `node:timers/promises` in a handler is not a durable Timer.

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
    await chargeCard(order, `${context.flowId}:charge`, context.cancellationSignal);
    orderStatus.set(context, "charged");
    return gracefulComplete(order.orderId);
  }
}

class RecordChargeFailure implements Step<Order> {
  public readonly inputCodec = orderCodec;

  public getStepType(): string {
    return "RecordChargeFailure";
  }

  public execute(context: Context, _order: Order): StepDecision {
    orderStatus.set(context, `charge-failed: ${context.recoveryError?.detail ?? ""}`);
    return gracefulComplete("charge-failed");
  }
}
```

- `getStepOptions()` holds method timeouts, `heartbeatTimeoutMs`,
  `waitForRetry`/`executeRetry` (`RetryPolicy`), durability, locks, selective
  loads, `waitForFailure: "proceed"`, and `executeFailure`.
- `ExecuteFailure.proceedTo(Target)` routes exhausted retries to a registered
  recovery Step, which receives the failing Step's input.
- Durability is `"sync"` or `"async"`. Set a Flow-wide default with
  `StartFlowOptions.configOverride.stepDurability`. Override one method with
  `waitForDurability` or `executeDurability`.
- Defaults and units are in the installed declarations.

## Heartbeat and cancellation

- Take `AsyncContext` and `await context.recordHeartbeat(value, codec?)`.
  Read the last checkpoint with `context.getLastHeartbeatValue(codec?)`, with
  the same codec. `context.hasLastHeartbeatValue()` tells absent from `null`.
- `stream.write` counts as liveness but does not replace a checkpoint.
- Check `context.cancellationSignal.aborted` between work units and pass the
  signal to `fetch` and other abort-aware calls.
- `context.recordEvent(name, value, codec)` adds a business milestone to Flow
  history.
- `context.setStepExecutionLocal(key, value, codec)` and
  `getStepExecutionLocal(key, codec)` pass a value from `waitFor` to
  `execute` of the same Step execution. Dex commits it with the method's
  result and stores it in the Step record, so `execute` reads it even on
  another Worker. Absence returns `undefined`. Only that Step execution can
  read it; use an Attribute for state another Step or a Client needs. See
  [Step execution local values](../core/step.md#step-execution-local-values).

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

- Define each Attribute, AttributeMap, Channel, ChannelMap, and Stream once, at
  module scope or as a Flow field. Register it in exactly one Flow's
  `getPersistenceSchema()`. Reuse the same object in handlers and Client calls.
- Handler access: `attribute.get/set/delete(context)`;
  `attributeMap.get/set/delete(context, instance)`,
  `getAllInstanceKeys(context)`, `getMapSize(context)`. Setting `null` or
  `undefined` deletes.
- AttributeMap instances load only when selected. StepOptions use the method
  prefix: `waitForLoadAttributeMaps`, `waitForLoadAttributeMapInstances`,
  `executeLoadAttributeMaps`, `executeLoadAttributeMapInstances`, and the same
  `waitForLoad*` / `executeLoad*` forms for Channels and ChannelMaps. RPCOptions
  and RPC invoke options use the unprefixed `loadAttributeMaps`,
  `loadAttributeMapInstances: [map.load(instance)]`, `loadChannels`, and
  `loadChannelMapInstances`. Reading an unselected instance throws
  `AttributeMapNotLoadedError`.
- `getAllInstanceKeys` and `getMapSize` need a whole-map load. To test one
  known key, load that instance and check `get(...) !== undefined`.
- Locks: `attribute.lock()` or `attributeMap.lock(instance)` in
  `waitForLockAttributes`, `executeLockAttributes`, or RPC `lockAttributes`.
  Read-modify-write of a map instance selects both `lock(instance)` and
  `load(instance)`.
- Index: `{ type: IndexType.KEYWORD, indexKey: "keyword1" }` as the third
  constructor argument. An indexed AttributeMap needs an explicit `indexKey`.
- `syncToAttributeStore()` projects the value to the stores named in
  `FlowConfig.attributeStoreNames`.
- `hardDeleteOnNull()` accepts only string and object codecs and cannot be
  combined with an index or `syncToAttributeStore()`. Pass the value at start
  with `InitialAttribute.of(apiKey, value)` in `StartFlowOptions.attributes`.
  An initial AttributeMap instance uses `InitialAttribute.mapValue(map,
  instance, value)`.
  `waitForAttributeMatch` rejects it.

## Channel and ChannelMap

- Publish with `channel.publish(context, value)` or
  `channelMap.publish(context, instance, value)`.
- Wait with `forOne()`, `forN(n)`, `atLeast`, `atMost`, or `range`. Read the
  consumed messages with `channel.results(context)`.
- Pending messages load only when selected: `loadChannels`, or
  `loadChannelMapInstances: [map.loadMessages(instance)]` on an RPC, and the
  `waitForLoad*` / `executeLoad*` forms in StepOptions. An RPC reads
  `pendingMessages` and `findPendingMessage` and calls
  `delete(context, messageId)`. ChannelMap forms take the instance too.
- `pendingMessages` returns the loaded snapshot. It ignores this handler's own
  staged `publish` and `delete` calls, so compute a returned queue from the
  snapshot and your changes.
- `isTransactional: true` on an RPC validates its deletions and commits its
  writes atomically. It does not lock the snapshot; use a shared Attribute
  lock for that.

## RPC

The `OrderFlow` methods above show `@rpc`. The decorator-free form works where
the runtime cannot transform decorators:

```typescript
export class NotebookFlow implements Flow<void> {
  public getFlowType(): string {
    return "NotebookFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.empty();
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [notes, apiKey, customerEmail], channels: [tenantCommands] };
  }

  public updateNote(context: Context, input: { readonly noteId: string; readonly text: string }): RPCResult<boolean> {
    const existed = notes.get(context, input.noteId) !== undefined;
    notes.set(context, input.noteId, { text: input.text });
    return { output: existed };
  }
}

declareRPCMethod(NotebookFlow, "updateNote", { outputCodec: booleanCodec });

export async function updateOneNote(client: Client, notebookFlow: NotebookFlow, flowId: string): Promise<boolean> {
  return client.invokeRPCWithOptions(
    notebookFlow.updateNote,
    flowId,
    { noteId: "n1", text: "hello" },
    {
      lockAttributeMapInstances: [notes.lock("n1")],
      loadAttributeMapInstances: [notes.load("n1")],
    },
  );
}
```

- An RPC is a public Flow method whose first parameter is `Context`. It
  returns `RPCResult<T>`, a Promise of it, or `void` for a procedure.
- The method name is the RPC name. Start it with an action verb.
  `RPCOptions.name` overrides it, for example to serve an RPC that another
  language's Flow defines.
- `declareRPCMethod(FlowClass, "method", options)` takes the same options as
  `@rpc`. Call it at module top level after the class and before building the
  Registry. Declare each method once, on the class that defines it. Arrow
  function fields cannot be RPCs. One Flow may mix both forms.
- Registration fixes codecs, `timeoutMs`, `lockAttributes`,
  `isTransactional`, and loads. `client.invokeRPCWithOptions` adds only
  `lockAttributeMapInstances`, `loadAttributeMapInstances`, and
  `loadChannelMapInstances` for one call.
- A Step decision cancels Steps Flow-wide with
  `withCancelingSteps(decision, StepA, ...)`, or only the siblings started by
  the same parent with `withCancelingSiblingSteps(decision, ...)`. Steps
  scheduled by the same decision or RPC result are never canceled by it.
- `RPCResult.nextSteps` schedules Steps. `RPCResult.cancelingSteps` cancels
  every current execution of the named Step types.
- A held lock fails the RPC with `RpcLockConflictError`.

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

export async function readProgress(client: Client, flowId: string, resumeToken: string) {
  const message = await client.readStream(flowId, progress, resumeToken, 30_000);
  const newestPage = await client.listStreamMessages(flowId, progress, 50, "");
  return { value: message.value, nextResumeToken: message.resumeToken, newestPage };
}
```

- Register the Stream in `getPersistenceSchema().streams`.
- `stream.write` sends immediately. `bufferedText` (string Streams only, in a
  Step) batches chunks and flushes before the result.
- `client.readStream` reads forward one message. Persist its `resumeToken`.
  `client.listStreamMessages` returns newest-first pages; pass `nextPageToken`
  back unchanged. The Server rejects an out-of-range page size with
  `INVALID_ARGUMENT` ([Server reference](https://docs.superdurable.io/reference/server/)).
- `client.writeStream(flowId, stream, source, value)` writes from outside a
  Flow ([error handling](error-handling.md#best-effort-stream-writes)).

## SubFlow

```typescript
class RunChildren implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly childFlow: Flow<number>) {}

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

- Register parent and child Flow instances in the same Registry. Pass the
  registered child instance to `SubFlow.run`.
- A failed child is a `FlowResult`, not an exception. Check `result.status`
  before `singleOutput(codec)`.

## Client

```typescript
export async function approveOrder(client: Client, orderFlow: OrderFlow): Promise<string> {
  await client.startFlow(orderFlow, "order-42", { orderId: "42", amountCents: 100 }, {
    requestId: "start-order-42",
    ignoreAlreadyStarted: true,
  });
  try {
    await client.invokeRPC(orderFlow.submitApproval, "order-42", "approved");
  } catch (error: unknown) {
    // A replay can find the Flow already closed; its terminal result decides.
    if (!(error instanceof FlowNotActiveOrNotFoundError) && !(error instanceof FlowClosingError)) {
      throw error;
    }
  }
  const result = await client.waitForFlow("order-42", 60_000);
  if (result.status !== "completed") {
    throw new Error(`order-42 ended ${result.status}`);
  }
  return result.singleOutput(stringCodec);
}
```

- The contract of `approveOrder` is the Flow's terminal result, so it waits
  with `waitForFlow`. `ChargeOrder` completes the Flow right after it writes
  `orderStatus`. A `waitForAttributeMatch` or `waitForStepCompletion` issued
  after the RPC can therefore find the Flow already closed and throw. Use those
  waits only for a milestone of a Flow that stays open, and handle the
  closed-Flow error by reading the terminal state.
- Replaying `approveOrder` after a lost response is safe. The start attaches to
  the existing run, the approval RPC may find the Flow closed, and the terminal
  result decides.

- `startFlow(flow, flowId, input, options)` resolves to the run ID once Dex
  accepts the start.
- `invokeRPC(flow.method, flowId, input?)` takes the bound method of the
  registered Flow instance. For an RPC without input, the third positional
  argument is the optional `runId`, not an input; never pass a payload there.
- `waitForFlow(flowId, timeoutMs?)` resolves to a `FlowResult` for a closed
  Flow and throws `LongPollTimeoutError` while it is open. Check
  `result.status` before `singleOutput(codec)`.
- `waitForAttributeMatch` (singleton, or map plus instance) and
  `waitForStepCompletion` take `requestTimeoutMs`, which bounds the whole
  call. `AttributeMatch` has `equalTo`, `notEqualTo`, `greaterThan`,
  `greaterThanOrEqual`, `lessThan`, and `lessThanOrEqual`.
- `describeFlow(flowId)` resolves to `FlowInfo` (`flowId`, `runId`,
  `flowType`, `status`, `startedAt`).
- `searchFlows(query, pageSize, nextPageToken)` searches indexed Attributes.
- `stopFlow`, `timeTravel`, `skipTimer`, and `updateFlowConfig` are operator
  actions.
- The Client has no direct Attribute or Channel write. Write through RPCs.

## Client Attribute reads

```typescript
export async function readOrderAndNote(client: Client, flowId: string) {
  const [status, note] = await client.getAttributes(flowId, orderStatus, notes.instance("n1"));
  return { status: status ?? "new", noteText: note?.text };
}
```

- `client.getAttributes(flowId, ...reads)` reads committed singleton
  Attributes and AttributeMap instances in one call, without a Worker,
  including from a retained closed run.
- Pass singleton Attributes, or map instances selected with
  `map.instance(key)`. `map.load(key)` does not compile here. Pass at least
  one read.
- The result is a typed tuple in read order. `undefined` means not set;
  `""`, `0n`, and `false` are set values.
- Each read must be the exact definition object that a registered Flow
  returns from `getPersistenceSchema()`. Another object with the same name
  fails with `FlowDefinitionError` before any request.
- Errors: [error handling](error-handling.md#client-errors).

Runnable primitives:
[examples/typescript/src/primitives](https://github.com/superdurable/dex/tree/main/examples/typescript/src/primitives),
[decorator-free RPC](https://github.com/superdurable/dex/tree/main/examples/typescript/decorator-free-rpc).
