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
In Kotlin, declare the Flow class and its RPC methods with `open`.

`Step<I>` declares `Class<I> getInputType()`; parameterized Step inputs are not
supported. Steps, attributes, and channels declare Java classes, not codecs:

```java
Attribute<String> status = Attribute.define("status", String.class);
Channel<Void> wakeup = Channel.define("wakeup", Void.class);
```

Wait factories read from the domain nouns they create:

```java
return Wait.until(
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
        .grpcErrorStatusMapping(GrpcErrorStatusMapping.newBuilder()
                .map(PaymentDeclinedException.class, Status.Code.FAILED_PRECONDITION)
                .build())
        .build();
```

`bindAddress` is the Java WorkerService listener. `serverAddress` is used only
to hydrate blob-backed values through Dex FlowService. `workerTarget` is the
address advertised in Flow configuration; when omitted, Worker derives it from
the bind address and exposes it through `worker.getWorkerTarget()`.

Every `Throwable` escaping application Step `waitFor`, Step `execute`, or RPC
code becomes a structured Worker error. The default gRPC status is `INTERNAL`,
including when application code throws `StatusException` or
`StatusRuntimeException`. Configure `GrpcErrorStatusMapping` only for exception
classes with a stable meaning to the owner of the Worker application. The most
specific mapped superclass wins; an unmapped class remains `INTERNAL`. These
diagnostic statuses do not change Step retry or failure policies. Capacity and
other Worker transport failures retain their own SDK-selected statuses.

Java Worker errors include the original exception type, detail, and stack trace,
including causes and suppressed exceptions. Dex persists at most 16 KiB of the
UTF-8 stack and appends a truncation marker without splitting a character. The
Worker log keeps the complete local stack.

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

## Exceptions

Public exceptions live in `io.superdurable.dex.exceptions`. Catch concrete
types for expected outcomes instead of comparing `ErrorSubStatus`:

```java
try {
    client.invokeRPC(stub::updateOrder, input);
} catch (FlowNotActiveException inactive) {
    // The Flow never existed or is already closed.
} catch (RpcLockConflictException conflict) {
    // Retry the RPC after lock contention.
} catch (WorkerInvocationException workerFailure) {
    log.error(
            "Worker {} failed: {}",
            workerFailure.getWorkerErrorType(),
            workerFailure.getWorkerErrorDetail());
}
```

`FlowNotFoundException` is returned by read and history operations such as
`describeFlow`, `getAttribute`, `waitForFlow`, and `resetFlow`. These operations
can read a closed Flow, so failure means no matching execution was found.
`FlowNotActiveException` is returned by RPC, publish, mutation, stop, timer,
configuration, and step-wait operations that require an open Flow.

`FlowAlreadyStartedException` identifies duplicate starts.
`LongPollTimeoutException` identifies an expected long-poll timeout.
`WorkerInvocationException` preserves the original WorkerService error type,
detail, gRPC code, and Java stack trace. Read the persisted trace with
`getWorkerStackTrace()`; it may be empty for Workers implemented by another SDK.
`DexServiceException` remains the generic service failure and exposes status
metadata for diagnostics.

Local definition and value failures use `FlowDefinitionException`,
`InvalidStepResultException`, and `ValueMappingException`.

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

The IWF integration inventory is implemented as real Dex E2E tests under
[`src/test/java/io/superdurable/dex/integ`](src/test/java/io/superdurable/dex/integ/README.md).
They run Java Client and Java Worker against `dexcli dev`, with Rust used only
for BlobCache. They cover flows, steps, RPCs, persistence, reset, timers,
failure modes, and options.

## Runtime architecture

Java owns Registry reflection, FlowService client transport, WorkerService gRPC
transport, callback dispatch, and exception mapping. Worker requests never
cross JNI. Synchronous Step and RPC handlers run on a bounded JVM executor so
they do not block gRPC event-loop progress. Java failures are logged with their
complete stack and returned with their Java type, message, mapped status, and
bounded persisted stack.

Client and Worker accept the Java `BlobCache` interface. The default
`BlobCache.open(...)` implementation is the shared Rust DXBC cache. Blob-backed
values are hydrated in Java: cache hits are decoded locally, while misses use
FlowService `LoadBlobs` and populate the cache.

The Maven artifact includes BlobCache native libraries for Linux x86-64 and
ARM64, macOS x86-64 and ARM64, and Windows x86-64. The SDK extracts the matching
library from `META-INF/native` automatically. Use the absolute path in
`-Ddex.blobCache.nativeLibrary=...` only to override the packaged library.
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
implementation 'io.superdurable:dex-sdk:0.0.3'
```

### Maven

```xml
<dependency>
    <groupId>io.superdurable</groupId>
    <artifactId>dex-sdk</artifactId>
    <version>0.0.3</version>
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

Run the JVM/native integration tests and all E2E scenarios against a fresh
`dexcli dev` environment:

```shell
./run-integration-tests.sh
```

The script builds `dexcli` from the current checkout and starts an isolated
Dex environment. Each Worker automatically synchronizes its registered Indexed
Attributes before listening. Gradle builds
the Rust BlobCache JNI library and starts a fresh Java Worker for each E2E case
with a unique worker port and flow ID. A clean checkout also requires Go 1.24+,
Node.js 22+, Rust 1.88+, and Temporal CLI.

### Measure integration coverage

Run the same integration suite with JaCoCo coverage:

```shell
./run-integration-tests.sh --coverage
```

The report combines the local JNI and WorkerService integration tests with all
`dexcli dev` E2E scenarios. It measures only production classes under
`io.superdurable.dex`; contract tests, other unit tests, test fixtures, and
generated protobuf classes are excluded. The XML report is written to
`build/reports/jacoco/integration/jacoco.xml`, and the browser report starts at
`build/reports/jacoco/integration/html/index.html`.

### Validate the publication

The validation command builds a Maven repository, verifies its JAR and POM,
then compiles and runs independent Gradle and Maven consumers. Both consumers
open the packaged Rust BlobCache library and perform a cache round trip.

```shell
./validate-publication.sh 0.0.3-local
```

The command requires JDK 17, Rust 1.88+, and Maven. Published classes still
target Java 8.

### Validate public API documentation

Every hand-written public API under `io.superdurable.dex` must have detailed
Javadoc. The documentation check ignores package-private runtime code and
generated protobuf classes, validates parameter and return tags, and renders
the same documentation shipped in the Maven Javadoc JAR:

```shell
./gradlew checkstyleMain javadoc --no-daemon
```

Open `build/docs/javadoc/index.html` to review the generated documentation.

### Publish a release

Maven Central publishing requires Portal user-token credentials for the
verified `io.superdurable` namespace and an ASCII-armored GPG private key. Set
the repository secrets `MAVEN_CENTRAL_USERNAME`, `MAVEN_CENTRAL_PASSWORD`, and
`GPG_SECRET_KEY`.

Publish a GitHub release whose tag is `sdk-java/vX.Y.Z`. The workflow derives
the immutable Maven version from that tag, builds all supported native
libraries, validates Gradle and Maven consumers, signs every artifact, and
closes and releases the Central staging repository. Manual dispatch requires
the same `X.Y.Z` version as an explicit input.

If you'd like to test your changes to the SDK with the workflows in the
[samples](https://github.com/superdurable/dex/tree/main/examples/java) repo,
use the local publishing command:

1. Run:
  ```
  ./gradlew publishToMavenLocal -PreleaseVersion=0.0.3-local
  ```

2. In the [samples](https://github.com/superdurable/dex/tree/main/examples/java)
   repo, depend on the same version passed with `-PreleaseVersion`. Then run:
  ```
   ./gradlew --refresh-dependencies build
  ```

3. Once you're done, to remove the locally published version, run:
  ```
  ./gradlew unpublishFromMavenLocal -PreleaseVersion=0.0.3-local
  ```

### Repo structure
* `.github/workflows/`: the GithubActions workflows
* IDL source lives in monorepo [`protos/dex.proto`](../protos/dex.proto) (see [`docs/design/idl-renames.md`](../docs/design/idl-renames.md))
* Generated stubs: `src/main/java/io/superdurable/gen/`
* `script/`: some scripts for GithubActions and testing
* `src/`: Java source code
  * `main/java/io/superdurable/dex/`: public SDK and Java runtime
  * `main/java/io/superdurable/gen/`: checked-in protobuf and gRPC stubs
  * `test/java/io/superdurable/dex/`: contract, transport, and integration tests
