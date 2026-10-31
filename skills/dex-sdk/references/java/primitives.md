# Java primitives

Choose the primitive from the behavior the application needs, not from a preferred API.

| Primitive | Java shape | Use it for | Key constraint |
| --- | --- | --- | --- |
| Flow | `Flow<I>` | Durable application boundary | `getSteps()` and schema define one stable Flow type |
| Step | `Step<I>` | Retryable side effects or durable decisions | Concrete, non-parameterized `Class<I>` input |
| Wait | `Wait.until`, `anyOf`, `allOf`, `anyCombinationOf` | Durable readiness | `waitFor` observes; `execute` acts after readiness |
| Attribute / AttributeMap | `Attribute.define`, `AttributeMap.define` | Durable latest state | Define once and register in the schema |
| Channel / ChannelMap | `Channel.define`, `ChannelMap.define` | Durable FIFO commands/events | A satisfied condition consumes selected messages |
| RPC | `@RPC` and `RPCResult<T>` | Synchronous command or query | Query-only handlers can read retained terminal runs; locks, transactions, and returned effects require active execution |
| Stream | `Stream.define` | Best-effort progress | Not authoritative state; clients resume with tokens |
| Timer | `Timer.byDuration`, `Timer.byTimestamp` | Durable deadlines | Timer readiness is not a Java sleep |
| SubFlow | `SubFlow.run` | Independently managed durable child work | Decide parent lifetime and cancellation explicitly |
| Client | `Client` | Lifecycle, committed Attribute reads, RPCs, Attribute-match waits, Streams, search | Catch concrete SDK exceptions |

## Wait composition

[Pinned wait example](https://github.com/superdurable/dex/blob/sdk-java/v1.31.0/examples/java/src/main/java/io/superdurable/dex/primitives/waittypes/WaitTypesFlow.java)
<!-- dex-source: examples/java/src/main/java/io/superdurable/dex/primitives/waittypes/WaitTypesFlow.java -->
```java
case "any":
    return Wait.anyOf(
            channelA.forOne("signal"),
            Timer.byDuration(timeout, "timeout"));
case "all":
    return Wait.allOf(
            channelA.forOne("signal-a"),
            channelB.forOne("signal-b"));
case "combo":
    return Wait.anyCombinationOf(
            ConditionCombination.of(
                    channelA.forOne("signal-a"),
                    Timer.byDuration(timeout, "timeout")),
            ConditionCombination.of(channelB.forOne("signal-b")));
```

Among ready `Wait.anyOf` candidates, Dex uses canonical Timer, Channel, then SubFlow order and preserves argument order within each kind. An earlier unready Condition does not block a later ready one. Only the winning Channel consumes messages. For strict priority, return only the current higher-priority Condition until it resolves.

Use condition IDs when code must distinguish winners, and for every condition inside `anyCombinationOf`. Read Channel results only from the current execution's `Context`.

## Durable state and locking

[Pinned Attribute example](https://github.com/superdurable/dex/blob/sdk-java/v1.31.0/examples/java/src/main/java/io/superdurable/dex/primitives/attribute/AttributeFlow.java)
<!-- dex-source: examples/java/src/main/java/io/superdurable/dex/primitives/attribute/AttributeFlow.java -->
```java
@Override
public StepOptions getStepOptions() {
    return StepOptions.newBuilder()
            .addWaitForLock(AttributeLock.of(status))
            .addWaitForLock(AttributeLock.of(progress, "payment"))
            .addExecuteLock(AttributeLock.of(status))
            .addExecuteLock(AttributeLock.of(progress, "payment"))
            .build();
}
```

Locks coordinate only Steps and RPCs that request the same lock. They do not make external calls transactional. AttributeMap and ChannelMap instance names must be stable business keys. For a short-lived secret, such as an API key, use a [hard-delete Attribute](data-handling.md#hard-delete-attributes); it supports only string and object values and cannot be indexed or synced.

Pending-message reads inside a Step or RPC are invocation snapshots. Other handlers may consume, delete, or publish concurrently. Transactional execution validates selected deletions and commits writes atomically, but does not lock the whole snapshot. Read and write pending messages directly only when the operation explicitly tolerates that race. When a decision requires the queue to remain unchanged, every cooperating Step and RPC writer must use the same Attribute lock.

An RPC without Attribute locks or `isTransactional` can query a retained terminal run when it returns no durable effects. Locks, transactions, returned effects, or Server policy require an active execution. Use `describeFlow` when lifecycle status matters; RPC success alone does not prove the Flow is active.

## Stream reads

Use `Client.readStream` for forward, one-at-a-time, optionally long-polling consumption. Use `Client.listStreamMessages` for non-blocking newest-first pages. Pass the typed Stream directly, and pass `getNextPageToken()` unchanged until it is empty.

[Pinned runnable listing](https://github.com/superdurable/dex/blob/sdk-java/v1.31.0/examples/java/src/main/java/io/superdurable/dex/primitives/stream/StreamController.java)
<!-- dex-source: examples/java/src/main/java/io/superdurable/dex/primitives/stream/StreamController.java -->
```java
final StreamMessagesPage<String> page = client.listStreamMessages(
        workflowId,
        flow.progress,
        pageSize,
        beforePageToken);
```

The before-page token is exclusive and scope-bound. The first page uses an empty token. Listing is a best-effort retained snapshot: concurrent newer writes stay outside the older-page chain, while trimming may remove messages. A trimmed anchor returns an empty page. The server requires a positive page size and caps it at 1000 by default.

## Decisions and commit boundary

Return `StepDecision.goTo`, `goToMany`, `gracefulComplete`, `forceComplete`, `forceFail`, or `deadEnd` according to the intended lifecycle. Attribute writes and Channel publications are staged with the successful handler result. An exception discards that attempt's staged mutations. External side effects require idempotency because their success cannot be rolled back.

## Client interaction

Use `startFlow` for a new execution, `waitForFlow` for terminal status, [`getAttributes`](#client-attribute-reads) for committed Attribute values, a typed RPC stub for writes of Flow-owned Attribute and Channel state and for reads that need Flow code or pending Channel messages, and Attribute match for blocking observation. The Client has no direct write method. Use bounded waits at service boundaries and continue polling after a long-poll timeout. `searchFlows` is for indexed discovery, not coordination.

## Client Attribute reads

From Java SDK v1.31.0, `Client.getAttributes(flowId, reads...)` reads committed values of registered Attributes and AttributeMap instances in one FlowService request, without a Worker. Follow the shared [semantics and selection rule](../core/primitives.md#read-attributes-from-outside-a-flow). Pass singleton `Attribute` definitions, or AttributeMap instances selected with `AttributeMap.instance(key)`. There is no single-Attribute method; pass one read to read one value.

[Released Java Client Attribute read](https://github.com/superdurable/dex/blob/sdk-java/v1.31.0/sdk-java/README.md)
<!-- dex-source: sdk-java/README.md -->
```java
AttributeValues values = client.getAttributes(
        "order-42",
        orderFlow.orderStatus,
        orderFlow.notes.instance("n1"));
String status = values.get(orderFlow.orderStatus).orElse("new");
Optional<Note> note = values.get(orderFlow.notes.instance("n1"));
```

The call takes varargs or a `List<? extends AttributeRead<?>>` of reads built at runtime. The Client copies the list before the request.

[Released SDK integration test](https://github.com/superdurable/dex/blob/sdk-java/v1.31.0/sdk-java/src/test/java/io/superdurable/dex/integ/GetAttributesTest.java)
<!-- dex-source: sdk-java/src/test/java/io/superdurable/dex/integ/GetAttributesTest.java -->
```java
final List<AttributeRead<?>> listReads =
        new ArrayList<AttributeRead<?>>();
listReads.add(WORKFLOW.count);
listReads.add(WORKFLOW.quantities.instance(SET_QUANTITY_INSTANCE));
final AttributeValues listValues = client.getAttributes(flowId, listReads);
```

- **Result.** The call returns an immutable `AttributeValues`. `get(read)` returns `Optional<T>` typed by the read. An unset Attribute or map instance reads as `Optional.empty()`. A stored value that decodes to `null` also reads as `Optional.empty()`, because `Optional` cannot hold `null`. An empty string, `0`, and `false` are set values.
- **Lookup.** `get` throws `IllegalArgumentException` for a read that was not part of the call. That includes another `Attribute` object with the same name. A freshly created `notes.instance("n1")` for the same AttributeMap object and key finds the value. Repeated `get` calls return the same decoded object, so copy a mutable value before changing it.
- **Definition identity.** Each definition must be the exact object that a registered Flow's `getPersistenceSchema()` returns. Another object with the same name and value class fails with `FlowDefinitionException` before any request. So does a definition from a second `hardDeleteOnNull()` call. Share the Flow's definition fields with the Client code, as `orderFlow.orderStatus` does above.
- **Instance selector.** `AttributeMap.instance(key)` returns an `AttributeMapInstance<T>` for a Client read. It does not contact Dex. It throws `IllegalArgumentException` at once for a null or blank key or one that contains `/`. It is separate from the handler-side map loads (`@RPC(loadAttributeMapInstances = ...)` and `RPCInvokeOptions.Builder.addLoadAttributeMapInstance`) and from `get(context, key)` inside a Step or RPC.
- **Hard-delete and large values.** Hard-delete Attributes are readable, and a deleted value reads as `Optional.empty()`. The Client loads large values through LoadBlobs and its `BlobCache` before it decodes them with the Client's `ObjectMapper`.

The call is blocking. It throws these exceptions, and every message names the Flow ID:

- `IllegalArgumentException` before any request: a blank Flow ID, no reads, a null read, a read that is not an `Attribute` or `AttributeMapInstance`, or two reads of the same key.
- `FlowDefinitionException` before any request: no registered Flow declares that exact definition object.
- `FlowNotFoundException`: the Flow does not exist, or its latest run is being purged. A closed run is still readable, so the call never throws `FlowNotActiveOrNotFoundException`. At a business Get boundary, return the not-found result directly.
- `ValueMappingException`: a stored value cannot be loaded or decoded as its definition's value class.
- `DexServiceException`: another FlowService or LoadBlobs failure. It keeps the gRPC code and Dex sub-status. Its message and `getDetail()` start with the call context, such as `getAttributes Flow order-42 Attributes order-status`, before the Server detail. `IllegalStateException` reports a malformed Server response.

On any error the call returns no values. [Released Java Client API](https://github.com/superdurable/dex/blob/sdk-java/v1.31.0/sdk-java/src/main/java/io/superdurable/dex/Client.java) and [AttributeValues](https://github.com/superdurable/dex/blob/sdk-java/v1.31.0/sdk-java/src/main/java/io/superdurable/dex/AttributeValues.java)
