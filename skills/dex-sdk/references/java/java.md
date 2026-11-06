# Java applications

Load this page first for any Java Dex work. It covers project shape,
registration, typing, the Worker and Client lifecycle, and Java-specific rules.
Concepts live in core; this page only says how to express them in Java.

| Task | Read next |
| --- | --- |
| Write Flows, Steps, waits, state, RPCs, Streams, SubFlows, Client calls | [primitives.md](primitives.md) |
| Handler failures, retries, recovery, Client exceptions | [error-handling.md](error-handling.md) |
| Integration tests against a real Dex Server | [testing.md](testing.md) |
| Choose a Flow shape | [../core/patterns.md](../core/patterns.md), then the pattern table in [primitives.md](primitives.md#patterns-in-java) |

## Version and API details

1. Find the resolved `io.superdurable:dex-sdk` version in the Gradle or Maven
   build. Use `./gradlew dependencyInsight --dependency io.superdurable:dex-sdk`
   or `mvn dependency:tree`.
2. Open that minor version in the
   [Java Developer reference](https://docs.superdurable.io/reference/java/)
   with the left-sidebar version selector. It holds option lists, defaults,
   limits, version differences, and the link to the generated Javadoc.
3. Check the Server requirement for that SDK minor in the
   [compatibility matrix](https://docs.superdurable.io/reference/).

This skill gives stable rules and skeletons. Method signatures and defaults
come from the Developer reference and the SDK doc comments. Never copy an API
from another language or invent one; if the installed SDK lacks a feature, say
so.

Runnable examples: [examples/java](https://github.com/superdurable/dex/tree/main/examples/java).

## Project shape

```text
src/main/java/com/acme/orders/
  OrderFlow.java          # schema, Steps, RPC methods
  OrderRequest.java       # input and output DTOs
  OrderController.java    # Client calls at the service boundary
  DexConfig.java          # Registry, BlobCache, Worker, Client
src/test/java/com/acme/orders/
  OrderFlowIntegTest.java # real Dex Server tests
```

- One Flow class per durable business process. Steps are nested classes of the
  Flow, so they can read the Flow's definition fields.
- Inject business dependencies (HTTP clients, repositories) through the Flow
  constructor. Steps reach them through the enclosing instance.
- Controllers own Flow ID choice and request IDs. Flows own durable state and
  decisions. Steps do bounded external work.

## Registration rules

- A Flow implements `Flow<StartInput>`. A Step implements `Step<I>`.
- `getSteps()` returns `StepList.startStep(first).otherSteps(...)`. List every
  Step the Flow can reach: targets of `goTo`, RPC movements, recovery Steps,
  and Steps only reached by a fallback movement. An RPC-only Flow returns
  `StepList.empty()` (the default).
- `getPersistenceSchema()` returns `PersistenceSchema.of(...)` with every
  Attribute, AttributeMap, Channel, ChannelMap, and Stream the Flow uses.
- Create one Step instance per Step class, as a final field of the Flow.
  Return the same instances from `getSteps()` on every call.
- Build one `Registry` from every Flow the process starts or executes,
  including SubFlow children: `new Registry(List.<Flow<?>>of(orderFlow, shipmentFlow))`.
- Registration validates names, the Step graph, persistence declarations,
  loads, locks, and RPCs. Treat a registration failure as a startup failure.

### Names are durable identifiers

| Name | Default | Rule |
| --- | --- | --- |
| Flow type | simple class name | Name the class after its domain, such as `OrderFlow`. Never name it `Flow`. The package does not disambiguate two classes with the same simple name. |
| Step type | simple class name | Unique within the Flow. |
| RPC name | Java method name | Start with a verb: `getOrderStatus`, `approveOrder`. Override with `@RPC(name = ...)` only to keep an old name. |
| Attribute, Channel, Stream names | the `define` argument | Stable strings, unique within the Flow. Never derive them from runtime data; use an AttributeMap or ChannelMap for dynamic keys. |

Open Flows keep running against new code. Renaming a class changes its
default Flow or Step type. Override `getFlowType()` or `getStepType()` with the
old name, or keep the old class registered until no open Flow can reach it.
The same holds for definition names, RPC names, and DTO field names. See
[../core/application.md](../core/application.md) for the versioning rules.

### RPC classes

The Client builds an RPC stub by subclassing the Flow class without calling
its constructor. A Flow class with `@RPC` methods must not be `final`, and its
`@RPC` methods must not be `final`. In Kotlin, declare both `open`. An `@RPC`
method is `public`, takes `Context` and at most one input, and returns `void`
or `RPCResult<O>`.

### Definition objects

Define persistence objects as `public final` fields of the Flow (or
`static final` constants). Client calls that take a definition, such as
`getAttributes`, `waitForAttributeMatch`, and `writeStream`, require the exact
object the registered Flow returned from `getPersistenceSchema()`. Reach it
through the registered Flow instance, for example `orderFlow.orderStatus`. A
second `Attribute.define(...)` with the same name is a different object and
fails with `FlowDefinitionException`. Builder calls such as
`hardDeleteOnNull()` and `syncToAttributeStore()` return a new object; keep and
register the returned one.

## Types and serialization

- `Step<I>.getInputType()` must return a concrete `Class<I>`. `List<Order>`
  cannot be expressed; wrap it in a named DTO or use `Order[].class`.
- Values cross the wire as JSON through Jackson. Use DTO classes with a
  no-argument constructor, or records. Keep property names stable while open
  Flows can decode them. Use `@JsonProperty` to keep a wire name when you
  rename a Java field.
- A Client and the Worker that serve the same Flows must use the same
  `ObjectMapper` configuration. Set it once on `ClientOptions` and
  `WorkerOptions` if you customize it.
- Use `Void` for no input; pass `null`. Use wrapper types (`Integer`, `Long`),
  not primitives, as type arguments.
- Java `null` has meaning: writing `null` to an Attribute deletes it, and a
  `null` completion output is discarded. Return an explicit result DTO when
  "no output" and "empty result" must differ.
- Inside handlers, `Attribute.get` returns `null` for an absent value. Outside,
  `Client.getAttributes` returns `Optional`.

## Worker and Client bootstrap

One process usually hosts one Worker, one Client, and one shared `BlobCache`.
Every Worker is a pull Worker: it opens outbound connections to the Dex Server
and needs no inbound port.

```java
public final class DexRuntime implements AutoCloseable {
    private final BlobCache blobCache;
    private final Worker worker;
    private final Client client;
    private final Thread workerThread;
    private final CompletableFuture<Void> workerResult; // fails when start() throws
    // private constructor and accessors omitted

    public static DexRuntime start(String serverAddress, String cacheDirectory, List<Flow<?>> flows)
            throws InterruptedException, TimeoutException {
        Registry registry = new Registry(flows);
        BlobCache blobCache = BlobCache.open(new BlobCacheConfig(cacheDirectory, 1L << 30));
        Worker worker = new Worker(registry, blobCache, WorkerOptions.newBuilder()
                .serverAddress(serverAddress)
                .pull(PullWorkerOptions.newBuilder().maxConcurrentTasks(16).build())
                .build());
        Client client = new Client(registry, blobCache, new ClientOptions(serverAddress));
        CompletableFuture<Void> workerResult = new CompletableFuture<>();
        Thread workerThread = new Thread(() -> {
            try {
                worker.start();
                workerResult.complete(null);
            } catch (Throwable failure) {
                workerResult.completeExceptionally(failure);
            }
        }, "dex-worker");
        workerThread.start();
        DexRuntime runtime = new DexRuntime(blobCache, worker, client, workerThread, workerResult);
        try {
            worker.waitPullRegistered(Duration.ofSeconds(30));
        } catch (InterruptedException | TimeoutException | RuntimeException failure) {
            runtime.close();
            throw failure;
        }
        return runtime;
    }

    @Override
    // Call close after the service stops admitting new requests.
    public void close() throws InterruptedException {
        worker.close();
        workerThread.join();
        client.close();
        blobCache.close();
    }
}
```

Rules:

- `Worker.start()` blocks until the Worker stops. Run it on a dedicated thread
  and capture its failure; a startup failure must fail the service.
- Call `waitPullRegistered` before reporting the service ready or accepting
  traffic. It throws checked `InterruptedException` and `TimeoutException`.
  On `InterruptedException`, restore the interrupt flag.
- Use `worker.getPullRegistrationState().isReady()` for a readiness probe.
- `close()` (or `stop()`) lets running handlers finish and send their results.
- Shut down in this order: stop admitting new requests, close the Worker and
  join its thread, then close the Client and the `BlobCache`. Running handlers
  may still use the Client and the cache while they drain.
- Share one disk `BlobCache` between the Worker and Client. Never create one per
  request. Size its directory for active large payloads.
- `maxConcurrentTasks` bounds parallel handlers in this process. Optional
  reserved RPC slots keep RPCs responsive while Steps run. Sizing rules,
  readiness details, and shutdown behavior are in the
  [Java Workers reference](https://docs.superdurable.io/reference/java/workers).

With Spring, expose `Registry`, `BlobCache`, `Worker`, and `Client` as beans
with `destroyMethod = "close"`, and build the `Registry` from a `List<Flow<?>>`
bean. Start the Worker thread in a lifecycle bean and wait for registration
there. See
[DexConfig.java](https://github.com/superdurable/dex/blob/main/examples/java/src/main/java/io/superdurable/dex/config/DexConfig.java).

## Pull Workers and Dex credentials

- The Worker and Client take the same Dex Server address. There is no Worker
  bind address and no Worker routing configuration.
- A Worker serves only the Flow types in its Registry. During a rolling
  deploy, old and new Workers serve the same Flow type at once, so every
  change must stay compatible with open Flows.
- When the Server requires authentication, set `credential(...)` on both
  `ClientOptions` and `WorkerOptions`. Use the SDK's file-based credential for
  a rotated token file, or its API-key credential when the application holds
  an API key. Share one credential instance, and close it only after every
  Client and Worker that uses it has closed.
- TLS and credential options, token refresh, and error behavior are listed in
  the [Java Developer reference](https://docs.superdurable.io/reference/java/)
  and the [Server authentication reference](https://docs.superdurable.io/reference/server/authentication).
- Read [../core/application.md](../core/application.md) for routing, retries
  across Worker restarts, heartbeats, and shutdown guarantees.

## What Java does not have

- There is no Java Connector SDK. Do not invent a Java loader for Dex Web
  Connector configuration, and never put provider credentials in Flow input or
  an ordinary Attribute. Keep them behind the application's secret management.
  A short-lived secret that the Flow itself must hold belongs in a hard-delete
  Attribute. See [../core/connector.md](../core/connector.md).
- The FDG 2.0 management metadata (Summary, Display, Action, Indexed Attribute,
  group, explanation) is read from Go source only. Do not invent Java
  annotations for it. Java Flows still work with Dex Web, the CLI, and the
  Client.

## Java gotchas

- `Thread.sleep` in a handler holds a Worker slot. Use a Timer for business
  time that can be long.
- Never keep the `Context`, a `BufferedTextStream`, or loaded values in a field.
  They belong to one invocation.
- `waitForFlow(flowId, timeout)` throws `LongPollTimeoutException` when the
  Flow is still running. Check `FlowResult.getStatus()` before decoding output.

## Self-check

- [ ] Flow class named after its domain, not `final` if it has RPCs.
- [ ] Every reachable Step in `getSteps()`; every definition in `getPersistenceSchema()`.
- [ ] Step inputs are concrete classes; DTO wire names are stable.
- [ ] Client code uses the registered Flow's definition objects.
- [ ] Worker runs on its own thread; startup waits for registration and fails on error.
- [ ] One shared `BlobCache`; Client, Worker, and cache closed on shutdown.
- [ ] External effects in Steps are idempotent (see [error-handling.md](error-handling.md)).
- [ ] Integration tests run against a real Dex Server (see [testing.md](testing.md)).
