# dex-sdk (Java)

Java SDK for [Dex workflow engine](https://github.com/superdurable/dex)

See [samples](../examples/java) for how to use this SDK to build your workflow.

Maven coordinates: `io.superdurable:dex-sdk` (namespace for domain [superdurable.io](https://superdurable.io)).

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

To implement a workflow, the two most core interfaces are

* [Workflow interface](src/main/java/io/dex/core/ObjectWorkflow.java)
  defines the workflow definition

* [WorkflowState interface](src/main/java/io/dex/core/WorkflowState.java)
  defines the workflow states for workflow definitions

A workflow can contain any number of WorkflowStates.

See more in https://github.com/superdurable/dex#what-is-dex

## How to build & run

### Using IntelliJ

1. Protobuf IDL lives in monorepo [`protos/dex.proto`](../protos/dex.proto) (no submodule checkout needed).
2. In "Build, Execution, Deployment" -> "Gradle", choose "wrapper task in Gradle build script" for "Use gradle from".
3. Open Gradle tab, click "build" under "build" to build the project

## Development Guide

### Update IDL

Edit [`protos/dex.proto`](../protos/dex.proto), then run `make -C ../protos proto` to refresh checked-in stubs under `src/main/java/io/superdurable/gen/`.

### Local testing

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

# Development Plan

## 1.0

- [x] Start workflow API
- [x] Executing `start`/`decide` APIs and completing workflow
- [x] Parallel execution of multiple states
- [x] Timer command
- [x] Signal command
- [x] SearchAttribute
- [x] DataAttribute
- [x] StateExecutionLocal
- [x] Signal workflow API
- [x] Get workflow DataAttributes/SearchAttributes API
- [x] Get workflow API
- [x] Search workflow API
- [x] Cancel workflow API
- [x] Reset workflow API
- [x] InternalChannel command
- [x] AnyCommandCompleted Decider trigger type
- [x] More workflow start options: IdReusePolicy, cron schedule, retry
- [x] StateOption: WaitUntil/Execute API timeout and retry policy
- [x] Reset workflow by stateId/StateExecutionId

## 1.1

- [x] New search attribute types: Double, Bool, Datetime, Keyword array, Text
- [x] Workflow start options: initial search attributes

## 1.2

- [x] Skip timer API for testing/operation
- [x] Decider trigger type: any command combination

## 1.3

- [x] Support failing workflow with results
- [x] Improve workflow uncompleted error return(canceled, failed, timeout, terminated)

### 1.4

- [x] Support PROCEED_ON_FAILURE for WaitUntilApiFailurePolicy

### 2.0

- [x] Renaming some concepts/APIs with breaking changes(see releaste notes)
- [x] Support workflow RPC

### 2.1

- [x] Support caching on persistence

### 2.2

- [x] Support atomic conditional complete workflow by checking signal/internal channel emptiness

### 2.3

- [x] Support dynamic data/search attributes and internal/signal channel definition
- [x] Support state options overridden dynamically
- [x] Support describe workflow API

### 2.4

- [x] Support execute API failure policy
- [x] Support RPC persistence locking policy

### 2.5

- [x] Add waitForStateExecutionCompletion API

### 2.6

- [x] Small breaking changes to IdReusePolicy for fixing typo
