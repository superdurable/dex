# dex-sdk (Java)

Java SDK for [Dex workflow engine](https://github.com/superdurable/dex)

See [samples](../examples/java) for how to use this SDK to build your workflow.

Maven coordinates: `io.superdurable:dex-sdk` (namespace for domain [superdurable.io](https://superdurable.io)).

## New user contracts

The canonical rewrite API is under `io.superdurable.dex`. This phase includes
strongly typed workflow definitions, persistence handles, registry
validation, synchronous gRPC Client operations, and a Java gRPC Worker.
Rust is used only by the DXBC BlobCache JNI binding.

Java workflows and steps are interfaces, while RPCs keep the annotation and
typed-stub model:

```java
public final class IncrementStep implements Step<Long> {
    @Override
    public Class<Long> getInputType() {
        return Long.class;
    }

    @Override
    public StepDecision execute(Context context, Long input) {
        return StepDecision.gracefulComplete(input + 1L);
    }
}

public class CounterFlow implements Flow<Long> {
    private final IncrementStep start = new IncrementStep();

    @Override
    public StepList<Long> getSteps() {
        return StepList.startStep(start);
    }

    @RPC(name = "increment", timeoutSeconds = 10)
    public RPCResult<Long> increment(Context context, Long amount) {
        return RPCResult.of(amount + 1L);
    }
}

CounterFlow stub = client.newRpcStub(CounterFlow.class, flowId, runId);
long value = client.invokeRPC(stub::increment, 1L);
```

Flows exposing RPC methods and their annotated methods must not be `final`.
RPC stubs intercept those methods without invoking Flow constructors.

`Step<I>` declares `Class<I> getInputType()`; parameterized Step inputs are not
supported. Steps, attributes, and channels declare Java classes, not codecs:

```java
Attribute<String> status = Attribute.define("status", String.class);
Channel<Void> wakeup = Channel.define("wakeup", Void.class);
```

Wait factories read from the domain nouns they create:

```java
return Wait.allOf(
        Timer.byDuration(Duration.ofSeconds(1)));
```

`StepOptions.waitForMethodTimeout(...)` and `executeMethodTimeout(...)` bound
the two handler calls. Timer and channel conditions determine how long a Step
waits.

`ClientOptions` and `WorkerOptions` contain a default Jackson `ObjectMapper` and
accept a configured mapper when needed. Java does not expose a public Codec API.
Workers use a builder so optional transport settings remain readable:

```java
WorkerOptions options = WorkerOptions.newBuilder()
        .bindAddress(":8803")
        .serverAddress("localhost:8801")
        .objectMapper(objectMapper)
        .build();
```

`bindAddress` is the Java WorkerService listener. `serverAddress` is used only
to hydrate blob-backed values through Dex FlowService. `workerTarget` is the
address advertised in Flow configuration; when omitted, Worker derives it from
the bind address and exposes it through `worker.getWorkerTarget()`.

`Flow<StartInput>.getSteps()` returns `StepList<StartInput>`. Start with
`StepList.startStep(step)` and append heterogeneous Steps with `otherSteps(...)`.
Use `StepList.empty()` when a Flow has no Steps, or
`StepList.withoutStartStep(...)` when every Step is RPC-triggered. The generic
binding catches start-input mismatches during compilation; Registry also
validates the runtime classes with
`inputType.isAssignableFrom(registeredType)`.
Client result decoding takes the output class and uses the configured mapper:

```java
String output = client.waitForFlow("flow-123", String.class);
```

Persistence schemas use factory methods instead of builders. Definitions may
be passed directly, as one mixed list, or as separate attribute and channel
lists:

```java
PersistenceSchema.of(counter, commands);
PersistenceSchema.of(definitions);
PersistenceSchema.of(attributes, channels);
```

Initial values bind each persistence definition to its Java value type directly
in `StartFlowOptions.Builder`:

```java
StartFlowOptions options = StartFlowOptions.newBuilder()
        .addAttribute(status, "created")
        .addAttribute(items, "order-1", 1)
        .build();
```

The legacy IWF integration inventory is implemented as 58 real Dex E2E tests under
[`src/test/java/io/superdurable/dex/iwfcompat`](src/test/java/io/superdurable/dex/iwfcompat/README.md).
They run Java Client and Java Worker against `dexcli dev`, with Rust used only
for BlobCache. They cover flows, steps, RPCs, persistence, reset, timers,
failure modes, and options.

## Runtime architecture

Java owns Registry reflection, FlowService client transport, WorkerService gRPC
transport, callback dispatch, and exception mapping. Worker requests never
cross JNI. Synchronous Step and RPC handlers run on a bounded JVM executor so
they do not block gRPC event-loop progress. Java failures are logged with their
stack and returned with their Java type and message.

Client and Worker accept the Java `BlobCache` interface. The default
`BlobCache.open(...)` implementation is the shared Rust DXBC cache. Blob-backed
values are hydrated in Java: cache hits are decoded locally, while misses use
FlowService `LoadBlobs` and populate the cache.

The native library is loaded as `dex_blob_cache_jni`, or from the absolute path
in `-Ddex.blobCache.nativeLibrary=...`. Loading failures report this
configuration directly.
Application callbacks, Registry, Client, and Worker remain usable with another
`BlobCache` implementation and do not load JNI.

Measure warm cache hits and identical-value puts across JNI with an optimized
native library:

```bash
./gradlew blobCacheBenchmark -PblobCacheBenchmarkThreads=8
```

## License

[Super Durable Source License 1.0](LICENSE), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).

## Requirements

- Java 1.8+

## How to use

After publish, artifacts appear on
[Maven Central](https://repo1.maven.org/maven2/io/superdurable/dex-sdk/)
(and on [MVN Repository](https://mvnrepository.com/artifact/io.superdurable/dex-sdk) with some delay).
Javadoc: [javadoc.io](https://www.javadoc.io/doc/io.superdurable/dex-sdk/latest/index.html).

### Gradle

```gradle
implementation 'io.superdurable:dex-sdk:0.0.2'
```

### Maven

```xml
<dependency>
    <groupId>io.superdurable</groupId>
    <artifactId>dex-sdk</artifactId>
    <version>0.0.2</version>
</dependency>
```


## Concepts

Applications implement [`Flow<I>`](src/main/java/io/superdurable/dex/Flow.java)
and [`Step<I>`](src/main/java/io/superdurable/dex/Step.java). A Flow declares
all registered Steps and the optional start marker in one `getSteps()` list,
plus its persistence schema and annotated RPC methods. Registry validates these
definitions before Client or Worker startup.

## How to build & run

### Using IntelliJ

1. Protobuf IDL lives in monorepo [`protos/dex.proto`](../protos/dex.proto) (no submodule checkout needed).
2. In "Build, Execution, Deployment" -> "Gradle", choose "wrapper task in Gradle build script" for "Use gradle from".
3. Open Gradle tab, click "build" under "build" to build the project

## Development Guide

### Update IDL

Edit [`protos/dex.proto`](../protos/dex.proto), then run `make -C ../protos proto` to refresh checked-in stubs under `src/main/java/io/superdurable/gen/`.

### Local testing

Run the JVM/native integration tests and all 58 E2E scenarios against a fresh
`dexcli dev` environment:

```shell
./run-integration-tests.sh
```

The script builds `dexcli` from the current checkout, starts an isolated local
Temporal and Dex server, and registers the test search attributes. Gradle builds
the Rust BlobCache JNI library and starts a fresh Java Worker for each E2E case
with a unique worker port and flow ID. A clean checkout also requires Go 1.24+,
Node.js 22+, Rust 1.88+, and Temporal CLI.

If you'd like to test your changes to the SDK with the workflows in the [samples](https://github.com/superdurable/dex/tree/main/examples/java) repo, 
use the local publishing command:

1. Run:
  ```
  ./gradlew publishToMavenLocal -x signMavenJavaPublication
  ```

2. In the [samples](https://github.com/superdurable/dex/tree/main/examples/java) repo, make sure your `build.gradle` depends on the same version you just published. To find which version you published, open the SDK's `build.gradle` file and look for the `version = "x.y.z"` line near the bottom of the file. Then run:
  ```
   ./gradlew --refresh-dependencies build
  ```

3. Once you're done, to remove the locally published version, run:
  ```
  ./gradlew unpublishFromMavenLocal
  ```

### Repo structure
* `.github/workflows/`: the GithubActions workflows
* IDL source lives in monorepo [`protos/dex.proto`](../protos/dex.proto) (see [`docs/design/idl-renames.md`](../docs/design/idl-renames.md))
* Generated stubs: `src/main/java/io/superdurable/gen/`
* `script/`: some scripts for GithubActions and testing
* `src/`: Java source code
  * `main/java/io/dex/core/`: SDK code
    * `command/`: the command implementation
    * `communication/`: the communication implementation
    * `mapper/`: the mapper with IDL
    * `persistence/`: the persistence implementation
    * `validator/`: some validators
    * `Client.java`: the client implemntation
    * `...java` ...
  * `test/java/io/dex/`: Java test code (currently only integ test)
    * `spring/`: the integ test setup of using Spring as REST controller
    * `integ/`: the integration tests
      * `XyzTest.java`: a file for test cases
      * `xyz/`: the Dex workflow implementation for the integration test cases
