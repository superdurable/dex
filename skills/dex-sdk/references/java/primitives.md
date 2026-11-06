# Java primitives

How each Dex Primitive looks in Java. Read the matching core file for what the
Primitive guarantees. The skeletons below compile against the current Java SDK;
imports are omitted. Option lists and defaults are in the
[Java Developer reference](https://docs.superdurable.io/reference/java/).

| Primitive | Java shape | Core |
| --- | --- | --- |
| Flow | `class OrderFlow implements Flow<OrderRequest>` | [flow.md](../core/flow.md) |
| Step | nested `final class X implements Step<I>` with `waitFor` and `execute` | [step.md](../core/step.md) |
| Attribute, AttributeMap | `Attribute.define`, `AttributeMap.define` fields | [attribute.md](../core/attribute.md) |
| Channel, ChannelMap | `Channel.define`, `ChannelMap.define` fields | [channel.md](../core/channel.md) |
| RPC | `@RPC` method on the Flow, called through a Client stub | [rpc.md](../core/rpc.md) |
| Stream | `Stream.define`, `BufferedTextStream` | [stream.md](../core/stream.md) |
| Timer | `Timer.byDuration` Condition | [timer.md](../core/timer.md) |
| SubFlow | `SubFlow.run` Condition | [subflow.md](../core/subflow.md) |
| Client | `Client` methods and RPC stubs | [client.md](../core/client.md) |

## Flow and Steps

```java
public class OrderFlow implements Flow<OrderRequest> {
    public final Attribute<String> orderStatus = Attribute.define(
            "order-status",
            String.class,
            new AttributeIndex(AttributeIndex.Type.KEYWORD, "keyword1"));
    public final Attribute<Long> orderRevision = Attribute.define("order-revision", Long.class);
    public final AttributeMap<String> orderNotes = AttributeMap.define("order-notes", String.class);
    public final Channel<String> approvals = Channel.define("order-approvals", String.class);
    public final Stream<String> progress = Stream.define("order-progress", String.class, 1L << 20);

    private final PaymentService payments;
    private final AwaitApproval awaitApproval = new AwaitApproval();
    private final ChargePayment chargePayment = new ChargePayment();
    private final RecordChargeFailure recordChargeFailure = new RecordChargeFailure();

    public OrderFlow(PaymentService payments) {
        this.payments = payments;
    }

    @Override
    public StepList<OrderRequest> getSteps() {
        return StepList.startStep(awaitApproval).otherSteps(chargePayment, recordChargeFailure);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(orderStatus, orderRevision, orderNotes, approvals, progress);
    }

    final class AwaitApproval implements Step<OrderRequest> {
        @Override
        public Class<OrderRequest> getInputType() {
            return OrderRequest.class;
        }

        @Override
        public Wait waitFor(Context context, OrderRequest order) {
            return Wait.anyOf(
                    approvals.forOne(),
                    Timer.byDuration(Duration.ofHours(24), "approval-deadline"));
        }

        @Override
        public StepDecision execute(Context context, OrderRequest order) {
            if (context.hasTimerFired()) {
                return StepDecision.forceFail("approval deadline expired");
            }
            List<String> approvers = approvals.getConditionResults(context);
            orderStatus.set(context, "approved by " + approvers.get(0));
            return StepDecision.goTo(ChargePayment.class, order);
        }
    }
    // ChargePayment and RecordChargeFailure: see "StepOptions" and error-handling.md
}
```

- The start Step's input type equals the Flow's type argument. A Flow that
  only RPCs drive uses `StepList.withoutStartStep(...)`.
- Omit `waitFor` when the Step needs no wait. Dex then runs `execute`
  immediately without a WaitFor invocation. `Wait.skipImmediately()` is the
  explicit form.
- `waitFor` declares durable readiness. It may write Attributes, publish, and
  set Step execution local values, but it never calls an external system.
  External effects belong in `execute`. See the
  [WaitFor rule](../core/step.md#waitfor-versus-execute).
- `execute` returns exactly one `StepDecision`. Throwing an exception fails the
  attempt and discards everything the attempt staged.
- A Step class is the target of a decision: `StepDecision.goTo(ChargePayment.class, input)`.
  The class must be listed in `getSteps()`.

## Waits and Conditions

| Need | Java |
| --- | --- |
| One Condition | `Wait.until(condition)` |
| First ready of several | `Wait.anyOf(c1, c2, ...)` |
| All of several | `Wait.allOf(c1, c2, ...)` |
| First complete group | `Wait.anyCombinationOf(ConditionCombination.of(c1, c2), ConditionCombination.of(c3))` |
| No wait | `Wait.skipImmediately()` or omit `waitFor` |

- Condition factories: `channel.forOne()`, `forN(n)`, `atLeast(n)`, `atMost(n)`,
  `range(min, max)`; `channelMap.forOne(instance)` and friends;
  `Timer.byDuration(duration)`; `SubFlow.run(ChildFlow.class, input[, options])`.
- Channel, ChannelMap, and Timer factories have an overload taking a
  condition ID. A SubFlow takes its ID from
  `SubFlowOptions.newBuilder().conditionId(...)`. Leave Conditions unnamed in
  `until`, `anyOf`, and `allOf`; `execute` reads results from the Channel and
  from `hasTimerFired`, never by ID. Give an ID to every Condition inside
  `anyCombinationOf`, and to a Timer that `skipTimer` targets by ID, as the
  `approval-deadline` Timer above.
- Which ready Condition wins an `anyOf`, and which messages a Channel
  Condition consumes, follow [../core/step.md](../core/step.md). For strict
  priority, wait on the higher-priority Condition alone.
- Read results in `execute` from the same invocation's `Context`:
  `channel.getConditionResults(context)`, `context.hasTimerFired()` (or the
  index overload), `SubFlow.getConditionResults(context)`.

## Step decisions

| Decision | Java | Effect |
| --- | --- | --- |
| Next Step | `StepDecision.goTo(StepClass.class, input)` | Schedules one Step. |
| Fan out | `StepDecision.goToMany(StepMovement.of(A.class, a), StepMovement.of(B.class, b))` | Schedules several Steps at once. |
| End branch | `StepDecision.deadEnd()` | This branch stops; the Flow stays open. |
| Complete when siblings finish | `StepDecision.gracefulComplete(output)` | Flow completes after other active Steps settle. |
| Complete now | `StepDecision.forceComplete(output)` | Flow completes immediately; other Steps stop. |
| Complete if queues empty | `StepDecision.forceCompleteIfChannelsEmpty(output, fallbackMovement, channels...)` | Atomically completes, or runs the fallback when a Channel has messages. |
| Fail | `StepDecision.forceFail(reason)` | Flow fails with a business reason. |

- `StepMovement.of(StepClass.class, input, stepOptions)` overrides options for
  that one execution only.
- `.withCancelingSiblingSteps(X.class)` cancels executions of `X` scheduled by
  the same predecessor. `.withCancelingSteps(X.class)` cancels every execution
  of `X`. Steps created by the same decision are not canceled. These varargs
  methods produce an unchecked-generics warning; suppress it locally.

## StepOptions

Override `getStepOptions()` on the Step. WaitFor and Execute have separate
timeouts, retries, durability, locks, and loads.

```java
@Override
public StepOptions getStepOptions() {
    return StepOptions.newBuilder()
            .executeMethodTimeout(Duration.ofSeconds(30))
            .executeRetry(RetryPolicy.newBuilder().maximumAttempts(5).build())
            .executeDurability(StepDurability.SYNC)
            .addExecuteLock(AttributeLock.of(orderStatus))
            .onExecuteFailureProceedTo(RecordChargeFailure.class)
            .build();
}
```

- Timeouts: `waitForMethodTimeout`, `executeMethodTimeout`, `heartbeatTimeout`.
  The Server enforces a minimum heartbeat timeout; check the Server reference
  before choosing a small value.
- Retries: `waitForRetry`, `executeRetry` with `RetryPolicy.newBuilder()`
  (`initialInterval`, `backoffCoefficient`, `maximumInterval`,
  `maximumAttempts`, `totalDuration`). Bound retries whenever a recovery route
  must eventually run.
- Failure routes: `onExecuteFailureProceedTo(Recovery.class)` and
  `waitForFailure(WaitForFailurePolicy.PROCEED)`. See
  [error-handling.md](error-handling.md).
- Durability: `waitForDurability` and `executeDurability` take
  `StepDurability.DEFAULT`, `SYNC`, or `ASYNC`. Choose from
  [../core/step.md](../core/step.md); do not infer it from the timeout.
- A Flow-wide default durability goes in
  `StartFlowOptions.newBuilder().configOverride(FlowConfig.newBuilder().stepDurability(...).build())`.
- Locks: `addWaitForLock` / `addExecuteLock` with `AttributeLock.of(attribute)`
  or `AttributeLock.of(attributeMap, instance)`.
- Loads: `addWaitForLoad*` / `addExecuteLoad*` for AttributeMaps, AttributeMap
  instances, Channels, ChannelMaps, and ChannelMap instances. Reading a map or
  pending messages that were not loaded throws.

## Attributes

```java
public final Attribute<String> orderStatus = Attribute.define(
        "order-status", String.class, new AttributeIndex(AttributeIndex.Type.KEYWORD, "keyword1"));
public final Attribute<String> email = Attribute.define("email", String.class).syncToAttributeStore();
private final Attribute<String> apiKey = Attribute.define("api-key", String.class).hardDeleteOnNull();
public final AttributeMap<String> orderNotes = AttributeMap.define("order-notes", String.class);
```

- Handler access: `attribute.get(context)`, `set(context, value)`,
  `delete(context)`. Map access adds the instance key:
  `orderNotes.get(context, "n1")`, `set(context, "n1", value)`,
  `getMapSize(context)`, `getAllInstanceKeys(context)`. Size and keys need a
  whole-map load; to test one known key, load that instance and read it.
- Singleton Attributes are always readable. Map instances are readable only
  when loaded by the handler's options.
- Index: pass an `AttributeIndex` with a type and an explicit generic search
  key (`keyword1`, `text1`, `int1`). Keep business names on the Attribute and
  reuse typed keys across Flow types. Search limits are in the
  [Server search reference](https://docs.superdurable.io/reference/server/search).
- `syncToAttributeStore()` projects latest values to the Flow's configured
  Attribute Store (`FlowConfig.newBuilder().attributeStoreNames(...)`). The
  projection is asynchronous; never base a Flow decision on it. Jackson
  property names of an AttributeMap DTO become the store's columns. See the
  [Server Attribute Store reference](https://docs.superdurable.io/reference/server/attribute-store-sync).
- `hardDeleteOnNull()` is for a short-lived secret such as an API key. It
  allows only string and object values and cannot be indexed or synced.
  `delete(context)` or `set(context, null)` removes it permanently. Pass it at
  start with `StartFlowOptions.newBuilder().addAttribute(flow.apiKey, value)`.
  `waitForAttributeMatch` rejects it. See [../core/attribute.md](../core/attribute.md).
- Map instance keys are stable business keys: non-blank and without `/`.

## Channels

```java
public final Channel<String> approvals = Channel.define("order-approvals", String.class);
public final ChannelMap<String> tenantCommands = ChannelMap.define("tenant-commands", String.class);
```

- Publish from a handler: `approvals.publish(context, value)`,
  `tenantCommands.publish(context, instance, value)`. Outside the Flow, publish
  through an RPC; the Client has no direct publish.
- Consume by waiting: a satisfied Channel Condition removes the selected
  messages. Read them with `getConditionResults(context)`.
- Manage a queue explicitly only with a load (`addExecuteLoadChannel`, or
  `@RPC(loadChannels = ...)`): `pendingMessages(context)`,
  `findPendingMessage(context, id)`, `delete(context, messageId)`, `size(context)`.
- Pending-message reads are a snapshot, not a lock. See
  [../core/channel.md](../core/channel.md).
- An RPC that deletes a message and must abort when it is missing sets
  `@RPC(isTransactional = true, ...)`.

## RPCs

```java
@RPC
public void approveOrder(Context context, String approver) {
    approvals.publish(context, approver);
}

@RPC
public RPCResult<String> getOrderStatus(Context context) {
    String status = orderStatus.get(context);
    return RPCResult.of(status == null ? "new" : status);
}

@RPC(lockAttributes = {"order-revision"})
public RPCResult<Long> addOrderNote(Context context, OrderNote note) {
    orderNotes.set(context, note.noteId(), note.text());
    Long current = orderRevision.get(context);
    long revision = (current == null ? 0L : current) + 1L;
    orderRevision.set(context, revision);
    return RPCResult.of(revision);
}

@RPC
public RPCResult<String> requestReport(Context context, Integer pages) {
    return RPCResult.of("accepted", StepMovement.of(RenderReport.class, pages));
}
```

Calling an RPC:

```java
OrderFlow stub = client.newRpcStub(OrderFlow.class, flowId);
client.invokeRPC(stub::approveOrder, "alice");
String status = client.invokeRPC(stub::getOrderStatus);
```

- `@RPC` options: `name`, `timeoutSeconds`, `lockAttributes` (Attribute names),
  `lockAttributeMaps` (`@RPCAttributeMapLock(attribute = ..., instance = ...)`),
  `isTransactional`, and loads (`loadAttributeMaps`,
  `loadAttributeMapInstances` as `"MapName/instance"`, `loadChannels`,
  `loadChannelMaps`, `loadChannelMapInstances`).
- Locks and loads chosen at call time go in `RPCInvokeOptions` and the
  matching `invokeRPC` overload. For read-modify-write of one map instance, add
  the same instance as both lock and load:

```java
client.invokeRPC(stub::appendSummary, line, RPCInvokeOptions.newBuilder()
        .addLockAttributeMapInstance(orderFlow.orderNotes, bucket)
        .addLoadAttributeMapInstance(orderFlow.orderNotes, bucket)
        .build());
```

- Which RPCs can read a closed Flow is in [../core/rpc.md](../core/rpc.md).
- `RPCResult.of(output, movement)` starts a Step. `.withCancelingSteps(X.class)`
  cancels Steps Flow-wide; an RPC has no siblings.
- A lock conflict fails the call at once with `RpcLockConflictException`. Retry
  it with application backoff only if the RPC is idempotent.
- Keep RPC handlers short and free of external calls. Put external work in a
  Step started by the RPC's movement.

## Streams

```java
public final Stream<String> progress = Stream.define("order-progress", String.class, 1L << 20);

// inside execute
BufferedTextStream writer = BufferedTextStream.create(context, progress);
writer.write("page " + page + " rendered\n");
```

- `stream.write(context, value)` sends one message right away. Use
  `BufferedTextStream` for text deltas such as model tokens. Create one writer
  per invocation; it flushes when the handler finishes.
- Streams are best effort; keep authoritative state in Attributes.
- Readers use `client.readStream(flowId, stream, resumeToken, timeout)` for
  forward long-polling reads, and `client.listStreamMessages(flowId, stream,
  pageSize, beforePageToken)` for newest-first pages. Pass tokens back
  unchanged; an empty token starts from the beginning or newest page.
- The Server rejects a page size outside its allowed range with
  INVALID_ARGUMENT; it does not clamp it. The range is in the
  [Server reference](https://docs.superdurable.io/reference/server/configuration).
- External writers use `client.writeStream(flowId, stream, source, value)`.

## Timers

`Timer.byDuration(duration[, conditionId])` returns a durable Condition. It
survives Worker restarts. Use it for deadlines, reminders, and inactivity
windows. Never use `Thread.sleep` for business time. A Timer fires no earlier
than its duration; do not assume exact precision. `client.skipTimer` fires a
pending Timer early (tests and operator actions).

## SubFlows

```java
static final class StartShipment implements Step<String> {
    private static final SubFlowOptions OPTIONS = SubFlowOptions.newBuilder()
            .timeout(Duration.ofHours(1))
            .build();

    @Override
    public Class<String> getInputType() {
        return String.class;
    }

    @Override
    public Wait waitFor(Context context, String orderId) {
        return Wait.until(SubFlow.run(ShipmentFlow.class, orderId, OPTIONS));
    }

    @Override
    public StepDecision execute(Context context, String orderId) {
        FlowResult shipment = SubFlow.getConditionResults(context);
        if (shipment.getStatus() != FlowStatus.COMPLETED) {
            return StepDecision.forceFail("shipment ended " + shipment.getStatus());
        }
        return StepDecision.gracefulComplete(shipment.getSingleOutput(String.class));
    }
}
```

- Register the child Flow in the same Registry as the parent.
- `SubFlow.getConditionResults(context[, index])` returns a `FlowResult`; check
  `getStatus()` before decoding output. `SubFlow.getFlowId(context[, index])`
  returns the child Flow ID.
- Decide the child timeout, timeout policy, and reuse policy explicitly with
  `SubFlowOptions`.

## Flow timeout handler

Override `handleTimeout(Context)` to make the Flow's timeout run a business
decision instead of failing. Set the timeout in `StartFlowOptions.timeout`.
Configure the handler's retries, locks, loads, and failure route with
`FlowTimeoutHandlerOptions`. Keep it bounded and idempotent.

```java
@Override
public StepDecision handleTimeout(Context context) {
    orderStatus.set(context, "timed-out");
    return StepDecision.forceFail("order deadline expired");
}
```

## Heartbeats, cancellation, and invocation data

```java
@Override
public StepDecision execute(Context context, Integer pages) {
    BufferedTextStream writer = BufferedTextStream.create(context, progress);
    int firstPage = context.hasLastHeartbeatValue()
            ? context.getLastHeartbeatValue(Integer.class)
            : 0;
    for (int page = firstPage; page < pages; page++) {
        if (context.isCancellationRequested()) {
            return StepDecision.deadEnd();
        }
        try {
            Thread.sleep(100);
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            return StepDecision.deadEnd();
        }
        writer.write("page " + page + " rendered\n");
        context.recordHeartbeat(page + 1);
    }
    return StepDecision.gracefulComplete(pages);
}
```

- A long handler calls `context.recordHeartbeat(checkpoint)` within its
  heartbeat timeout. A Stream write also counts as a heartbeat. The next
  attempt reads the last checkpoint with `getLastHeartbeatValue(type)`.
- A checkpoint is resume state, not a commit. Record it only after the work it
  describes is complete or safe to repeat.
- `Context` also exposes `getFlowId()`, `getRunId()`, `getStepExecutionId()`,
  `getAttempt()`, and `getFirstAttemptAt()`. Use them for logs and idempotency
  keys.
- `context.recordEvent(name, value, Type.class)` adds a business milestone to
  Flow history.
- `setStepExecutionLocal` passes a value from `waitFor` to `execute` of the
  same Step execution. Dex commits it with the method's result and stores it
  in the Step record, so `execute` reads it even on another Worker.
  For an absent key, `getStepExecutionLocal` returns the type's default:
  `false` for `Boolean`, zero for numeric types (boxed or primitive), and
  `null` otherwise. Do not use a null check as a presence test for those
  types. Only that
  Step execution can read it; use an Attribute for state another Step or a
  Client needs. See [Step execution local values](../core/step.md#step-execution-local-values).

## Client

Use the Client at the service boundary only. Every call blocks.

| Need | Java |
| --- | --- |
| Start | `client.startFlow(flow, flowId, input[, StartFlowOptions])` returns the run ID |
| Idempotent start | `StartFlowOptions.newBuilder().requestId(stableId).ignoreAlreadyStarted(true)` |
| Call an RPC | `client.invokeRPC(client.newRpcStub(Flow.class, flowId)::method, input)` |
| Read committed Attributes | `client.getAttributes(flowId, reads...)` |
| Wait for a value | `client.waitForAttributeMatch(flowId, attribute, AttributeMatch.greaterThan(0L), options)` |
| Wait for a Step | `client.waitForStepCompletion(flowId, StepExecutionId.of("ChargePayment"), options)` |
| Wait for the end | `client.waitForFlow(flowId[, timeout])` |
| Current status | `client.describeFlow(flowId)` |
| Search | `client.searchFlows(query, pageSize)` |
| Stop | `client.stopFlow(flowId[, StopFlowOptions])` |

- Start options also set the timeout, ID reuse policy, initial Attributes,
  and config override.
- Never set a positive start delay (`startDelay`). The Server rejects it with
  INVALID_ARGUMENT. To delay the first work, start the Flow at once and have
  the start Step wait on a Timer.
- The Client has no direct write. Write Flow state through an RPC.
- Wait options take a request timeout. `RequestTimeoutException` means only
  the caller's budget ran out. See [../core/client.md](../core/client.md)
  for revision watermarks, `describeFlow`, and search rules.

## Client Attribute reads

`client.getAttributes(flowId, reads...)` reads committed values of registered
Attributes and AttributeMap instances in one request. It does not run Flow
code or need a Worker.

```java
AttributeValues values = client.getAttributes(
        flowId,
        orderFlow.orderStatus,
        orderFlow.orderNotes.instance("summary"));
Optional<String> status = values.get(orderFlow.orderStatus);
Optional<String> summary = values.get(orderFlow.orderNotes.instance("summary"));
```

- Pass varargs or a `List<AttributeRead<?>>`. Pass one read to read one value.
- `AttributeValues.get` returns the decoded object without copying it. Copy a
  mutable value before you change it.
- Each read must be the exact definition object of a registered Flow;
  otherwise `FlowDefinitionException` before any request.
  `attributeMap.instance(key)` selects a map instance for this call.
- `get(read)` returns `Optional`: unset and stored `null` are empty; `""`,
  `0`, and `false` are present. A read not in the call throws.
- A missing Flow throws `FlowNotFoundException`; a closed Flow stays
  readable until purge.
- When to prefer a read-only RPC is in [../core/client.md](../core/client.md).

## Patterns in Java

Choose the pattern in [../core/patterns.md](../core/patterns.md). The Java
building blocks:

| Pattern | Java shape | Example package |
| --- | --- | --- |
| Static parallel Steps | `goToMany(StepMovement.of(A.class, x), StepMovement.of(B.class, x))` | `patterns/parallel` |
| Dynamic parallel Steps | build `StepMovement<?>[]` from input | `patterns/parallel` |
| Await all Steps | branches publish to a Channel and `deadEnd()`; joiner waits `forN(count)` | `patterns/parallel` |
| First win | winner returns `.withCancelingSiblingSteps(Same.class)` | `patterns/parallel` |
| Parallel SubFlows | `Wait.allOf(SubFlow.run(...), ...)` | `patterns/parallelsubflows` |
| Polling | one long `execute` loop with heartbeat and bounded retry | `patterns/polling` |
| Cron, reminder, inactivity | Timer raced with Channels in `Wait.anyOf` | `patterns/cron`, `patterns/reminders`, `patterns/inactivenesstracker` |
| Inbound event loop | RPCs publish; one Step waits on `Wait.anyOf(replies.atLeast(1), resolutions.forOne(), Timer)`, appends to the transcript, and loops back ([rules](../core/patterns.md#inbound-event-loop)) | closest: `patterns/inactivenesstracker` |
| Execute recovery | `onExecuteFailureProceedTo(Recovery.class)` | `patterns/recovery` |
| WaitFor recovery | `waitForFailure(WaitForFailurePolicy.PROCEED)` plus `context.waitForMethodFailed()` | `primitives/proceedonwaitfailure` |
| Manual recovery | recovery Step waits on retry and skip Channels | `patterns/intervention` |
| Graceful timeout | `handleTimeout` plus `StartFlowOptions.timeout` | `patterns/timeout` |
| Drain internal Channel | side Step loops until a final marker | `patterns/drainchannels` |
| Drain external publishing | `forceCompleteIfChannelsEmpty(output, fallback, channel)` | `patterns/drainchannels` |
| Interruptible | RPC cancels running Steps; handlers check cancellation | `patterns/interruptible` |
| Wait for Step completion | `client.waitForStepCompletion` after start | `patterns/waitforstepcompletion` |
| Wait for Attribute match | locked revision Attribute plus `waitForAttributeMatch` | `products/jobpost` |
| Stream progress | `BufferedTextStream` plus `client.readStream` | `primitives/stream` |
| Entity store | long-lived RPC-driven Flow per entity | `patterns/entitystore` |
| Chunked AttributeMap | bounded `current` instance plus immutable archives | `patterns/sequentiallychunkedattributemap` |
| Hash-partitioned AttributeMap | stable hash picks a bucket; invocation lock and load | `patterns/hashpartitionedattributemap` |

Packages are under [examples/java/src/main/java/io/superdurable/dex](https://github.com/superdurable/dex/tree/main/examples/java/src/main/java/io/superdurable/dex). Copy the shape, not the business types.

### Drain an externally published queue

```java
@Override
public Wait waitFor(Context context, Void input) {
    return Wait.until(externalQueue.forOne());
}

@Override
public StepDecision execute(Context context, Void input) {
    for (String item : externalQueue.getConditionResults(context)) {
        context.recordEvent("drained", item, String.class);
    }
    return StepDecision.forceCompleteIfChannelsEmpty(
            "drained",
            StepMovement.of(DrainQueue.class, null),
            externalQueue);
}
```

### Polling an external job

Follow the polling rules in [../core/patterns.md](../core/patterns.md). In Java:
one `execute` loops; it calls the provider with its own timeout, keeps "not
ready" inside the loop, throws only transient errors, writes a Stream message
or `recordHeartbeat` each round, then `Thread.sleep(interval)`. Restore the
interrupt flag on `InterruptedException`. Bound the whole wait with
`executeMethodTimeout`, `RetryPolicy.totalDuration`, and a failure route. Keep
`interval + call timeout <= heartbeatTimeout - 10 s`: with the one-minute
default and a 10-second call timeout, the interval is at most 40 seconds. Keep
the one-minute default unless a healthy call is silent longer. Keep
`maximumAttempts` small, such as 3 to 5; `totalDuration` bounds the time, never
a large attempt count. Never loop through a
WaitFor Timer, `RetryAfterException`, or a hand-written deadline. Copy the
shape from
[PollingFlow](https://github.com/superdurable/dex/blob/main/examples/java/src/main/java/io/superdurable/dex/patterns/polling/PollingFlow.java).
