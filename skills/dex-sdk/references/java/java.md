# Java handbook

The SDK API reference uses the [released v1.26.0 baseline](../core/bundle-baselines.md); the source examples' older dependency pins below remain factual and do not define the current error API.

Use this page first for Java application work. Then load the topic page that matches the task. The [baseline build](https://github.com/superdurable/dex/blob/sdk-java/v1.26.0/examples/java/build.gradle) uses `io.superdurable:dex-sdk:1.25.0`, Spring Boot, and JDK 17 or newer. The application's Gradle or Maven lockfile remains authoritative.

## Project shape

Keep Flow and Step types in application packages, expose them as Spring beans, build one `Registry` from every Flow bean, and share one disk `BlobCache` between the `Worker` and `Client`. Inject business dependencies through constructors. Parameterized Step inputs are not supported directly; use a concrete holder class or an array.

The official example layout separates `products/`, `patterns/`, `primitives/`, and `shared/`. A smaller service normally needs `flows/`, `steps/`, `model/`, and a bootstrap/configuration class.

## Pull Workers and Dex credentials

From Java SDK v1.25.0, every Worker is a pull Worker. Set `WorkerOptions.Builder.serverAddress(...)` on the Worker and pass the same address to `new ClientOptions(serverAddress)`; no Worker address or target is configured, and StartFlow and SubFlow always route to pull Workers. v1.25.0 removed `WorkerTarget`, the Worker bind address, `worker.getWorkerTarget()`, and the Worker target options on ClientOptions and FlowConfig; delete them when upgrading. `updateFlowConfig` does not change a Flow's Worker routing. The pull options default to `PullWorkerOptions.newBuilder().build()`; use `WorkerOptions.newBuilder().pull(PullWorkerOptions.newBuilder().maxConcurrentTasks(8).build())` to tune them. `PullWorkerOptions.Builder` also offers `idleStreamsPerQueue`, `appWorkerID`, and `stopDrainTimeout`; defaults are one idle stream per queue, a generated process ID, and 30 seconds. A supplied app Worker ID must change after restart. With Java SDK v1.12.0, a Worker that lacks a requested Step type or RPC name declines it before the handler runs, so Dex can reassign the task without consuming the Step attempt. Read the [pull Worker guarantees](../core/pull-workers.md) for sticky routing, retry consumption, heartbeats, capacity, and Stop. [Released Java options](https://github.com/superdurable/dex/blob/sdk-java/v1.26.0/sdk-java/src/main/java/io/superdurable/dex/PullWorkerOptions.java) [Runnable pull Worker example](https://github.com/superdurable/dex/blob/sdk-java/v1.26.0/examples/java/src/main/java/io/superdurable/dex/production/pullworkers/PullGreetingExample.java) selects SDK v1.25.0.

From Java SDK v1.23.0, a queue that declines a Step for reserved RPC capacity keeps one extra idle stream for 30 seconds; set `PullWorkerOptions` idle streams per queue to at least two for heavy Step backlogs. The Worker also sends an internal upstream Pong every 20 seconds while a task runs, so proxy idle timeouts must exceed 20 seconds and Server **matching.pingInterval**.

Java SDK v1.14.0 adds **worker.waitPullRegistered(Duration.ofSeconds(30))**. Run blocking **start()** on another thread, handle its failures, and wait before activating the Worker. Timeout throws **TimeoutException**; interruption ends only the wait. Probe **worker.getPullRegistrationState().isReady()**. Queue entries expose **getQueue()**, **hasRegisteredStream()**, and **getLastPermanentError()**. A **PullRegistrationError** preserves the queue through **getQueue()** and transport error through **getCause()**. Opt into **PullWorkerOptions.Builder.stopOnPermanentStreamRejection(true)** to drain and throw that error from **start()** when a permanent rejection leaves a queue without another registered stream. Always call **stop()** or **close()** afterward. Follow the shared [readiness contract](../core/pull-workers.md#startup-registration-and-readiness), including stable readiness across reconnects and Stop. [Released Java readiness API](https://github.com/superdurable/dex/blob/sdk-java/v1.26.0/sdk-java/src/main/java/io/superdurable/dex/Worker.java)

With SDK v1.15.0, **PullWorkerOptions.Builder.reservedRpcTasks** reserves RPC capacity. Omit the builder setting for the default; pass **0** to opt out. Unset reserves one slot when resolved maximum concurrent tasks **M ≥ 2**; otherwise it reserves zero and logs one startup warning. Require **0 ≤ R < M**; invalid reserves fail construction. This intentionally gives Steps one fewer slot by default. Both API-direct and transactional RPC tasks may use all **M** slots; WaitFor and Execute, including local attempts, are declined at **M − R** total busy handlers. Export **Worker.getPullReservedRpcDeclines()** to measure reserve declines. Follow the shared [reserve semantics and sizing advice](../core/pull-workers.md#reserved-rpc-capacity) for dispatch exhaustion, counter scope, and long-running Steps. [Released capacity guidance](https://github.com/superdurable/dex/blob/sdk-java/v1.26.0/sdk-java/README.md)

With SDK v1.14.1, the Worker gracefully closes surplus idle streams after load drops while preserving readiness and the configured registered idle streams per queue. Size steady-state streams as **M + (F + 1) × I**, where **M** is maximum concurrent tasks, **F** is registered Flow types, and **I** is idle streams per queue. Burst peaks remain bounded by **matching.maxConcurrentTasksCap** and **matching.maxStreams**; follow the shared [capacity guidance](../core/pull-workers.md#heartbeats-capacity-and-shutdown).

Set `credential(...)` separately on ClientOptions and WorkerOptions when public API authentication is enabled. `FileCredential` reads a rotating token file; a custom `Credential.token()` supplies the current bearer token for each call and pull stream opening. A credential error prevents the call from reaching Dex. Protect the plaintext connection. [Released Java credential API](https://github.com/superdurable/dex/blob/sdk-java/v1.26.0/sdk-java/src/main/java/io/superdurable/dex/FileCredential.java)

With Java SDK v1.16.0, use **ApiKeyCredential** when the application holds an API key instead of a rotated token file. **ApiKeyCredential.builder(URI tokenUrl, String serverId, String apiKey)** throws **IllegalArgumentException** for a missing value or a disallowed URL. Its optional **exchangeTimeout(Duration)** defaults to ten seconds. Its optional **httpConnectionFactory(...)** opens proxy or test connections; the factory stays caller-owned, and the credential disconnects each connection after use. **build()** does not contact the token endpoint; the first call performs the exchange. Pass the same instance to **credential(...)** on ClientOptions and WorkerOptions. The class is **AutoCloseable** and owned by the application. Close it only after every Client and Worker sharing it has closed, because later token calls throw **IllegalStateException**. A direct **token()** call throws **ApiKeyCredential.ExchangeException** on a failed exchange. It is an **IOException** whose **getHttpStatus()** is -1 when no response arrived; its message redacts secrets and omits transport causes. Client calls report the failure as **DexServiceException** with UNAUTHENTICATED, so catch that around Client calls. A gRPC deadline or cancellation bounds a token wait during a unary retry. Follow the shared [API key Credential contract](../core/operations.md#api-key-credential) for refresh, rejection retries, and redaction. [Released Java API key credential](https://github.com/superdurable/dex/blob/sdk-java/v1.26.0/sdk-java/src/main/java/io/superdurable/dex/ApiKeyCredential.java) [README example](https://github.com/superdurable/dex/blob/sdk-java/v1.26.0/sdk-java/README.md)

## FlowService transport

Java SDK v1.20.0 **ClientOptions** and **WorkerOptions** have no FlowService TLS option. Their FlowService channels use plaintext gRPC, including pull streams. The Go SDK v1.22.0 option does not apply. See [FlowService TLS](../core/operations.md#flowservice-tls). [Client options](https://github.com/superdurable/dex/blob/sdk-java/v1.26.0/sdk-java/src/main/java/io/superdurable/dex/ClientOptions.java) · [Worker options](https://github.com/superdurable/dex/blob/sdk-java/v1.26.0/sdk-java/src/main/java/io/superdurable/dex/WorkerOptions.java)

## Connector runtime support

The Connector SDK's `projectconfig.LoadFromEnvironment` loader and the generated `NewProjectConnection` API are Go-only, and no equivalent Java SDK exists today. Do not invent a Java loader or deserialize Dex Web's credential file into Flow state. For a Java application, keep provider credentials behind an application-owned secret/runtime adapter until an official Java Connector SDK exposes the same contract.

## Deployed configuration and recovery boundary

Dex provides no deployed Connector configuration or credential store. A deployed Go
application reads its connections with the Connector SDK's `sdkgo/projectconfig`, but
no equivalent Java SDK exists today; see
[deployed Connector configuration](../core/operations.md#deployed-connector-configuration).
Keep credentials behind the deployment's secret management; a browser, Flow input,
or ordinary Attribute cannot select secret storage.
When the Flow itself must hold a short-lived secret, such as an API key passed
at start, use a [hard-delete Attribute](data-handling.md#hard-delete-attributes)
instead of an ordinary Attribute.
For recovery after a Worker fix, preserve the original resource and Flow ID,
reconcile external effects, and use an explicitly authorized, recorded Step
boundary as described in [safe recovery](../core/operations.md#safe-recovery).

## Dex Web v2 management metadata

The current FDG 2.0 management-interface analyzer reads Go source only. A Java
Flow cannot currently publish the Summary, Display, Action, Indexed Attribute,
group, explanation, or Action input capture metadata that drives the full Dex
Web v2 management interface. Java applications can still operate Flows and use
their supported Client and CLI surfaces; do not invent Java annotations for the
Go-only analyzer contract.

## Minimal Flow

[Pinned runnable source](https://github.com/superdurable/dex/blob/sdk-java/v1.26.0/examples/java/src/main/java/io/superdurable/dex/primitives/flow/ExampleFlow.java)
<!-- dex-source: examples/java/src/main/java/io/superdurable/dex/primitives/flow/ExampleFlow.java -->
```java
@Component
public class ExampleFlow implements Flow<Integer> {
    public static final Attribute<String> status = Attribute.define("status", String.class);
    public static final Channel<Void> notify = Channel.define("notify", Void.class);

    private final ExampleStep exampleStep = new ExampleStep();
    private final Finish finishStep = new Finish();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(exampleStep).otherSteps(finishStep);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(status, notify);
    }
```

A Flow returns its complete Step registry once. The first Step input type must match `Flow<I>`. All persisted definitions belong in `getPersistenceSchema()`.

[Pinned Step source](https://github.com/superdurable/dex/blob/sdk-java/v1.26.0/examples/java/src/main/java/io/superdurable/dex/primitives/flow/ExampleFlow.java)
<!-- dex-source: examples/java/src/main/java/io/superdurable/dex/primitives/flow/ExampleFlow.java -->
```java
final class ExampleStep implements Step<Integer> {
    @Override
    public Class<Integer> getInputType() {
        return Integer.class;
    }

    @Override
    public Wait waitFor(final Context context, final Integer input) {
        status.set(context, "running");
        return Wait.skipImmediately();
    }

    @Override
    public StepDecision execute(final Context context, final Integer input) {
        return StepDecision.goTo(Finish.class, input + 1);
    }
}
```

## Registry, Worker, and Client

[Pinned bootstrap source](https://github.com/superdurable/dex/blob/sdk-java/v1.26.0/examples/java/src/main/java/io/superdurable/dex/config/DexConfig.java)
<!-- dex-source: examples/java/src/main/java/io/superdurable/dex/config/DexConfig.java -->
```java
@Bean
public Registry registry(final List<Flow<?>> flows) {
    return new Registry(new ArrayList<Flow<?>>(flows));
}

@Bean(destroyMethod = "close")
public BlobCache blobCache(
        @Value("${dex.blob-cache-dir}") final String blobCacheDir) {
    return BlobCache.open(new BlobCacheConfig(blobCacheDir, 1L << 30));
}
```

Start the blocking Worker on a managed thread and close Worker, Client, and BlobCache during application shutdown. Do not create a cache per request.

Add `io.superdurable:dex-sdk:<resolved-version>` to Gradle or Maven. Application adapters call the injected Client to start a registered Flow, invoke typed RPC stubs for Flow-owned state, wait for Attribute matches, or wait for terminal status. The Java SDK has no Client Attribute read yet, so read Flow state through a typed read-only RPC; see [Client interaction](primitives.md#client-interaction).

## Run locally

```bash
dexcli dev
cd examples/java
./gradlew bootRun
```

Use `./run-integration-tests.sh` for the official application suite. Defaults connect to `localhost:8801`; configure the server address and cache directory with the example environment variables. The examples always run a pull Worker, which needs only an outbound connection to Dex. See the [pinned example README](https://github.com/superdurable/dex/blob/sdk-java/v1.26.0/examples/java/README.md).

## Continue reading

- [Primitives](primitives.md) for application building blocks.
- [Patterns](patterns.md) before selecting a Flow shape.
- [Errors](error-handling.md) before writing Client, admission, RPC, cleanup, wait, or external Stream boundaries.
- [Testing](testing.md) before declaring durable behavior complete.
- [Data](data-handling.md), [observability](observability.md), and [versioning](versioning.md) for production behavior.
- [Gotchas](gotchas.md) and [advanced features](advanced-features.md) for constraints that are easy to miss.
