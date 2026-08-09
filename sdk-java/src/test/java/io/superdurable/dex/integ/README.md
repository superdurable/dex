# Java integration tests

This suite ports the integration scenario inventory from
[`indeedeng/iwf-java-sdk`](https://github.com/indeedeng/iwf-java-sdk/tree/8fa04457c0abcc4473300f17ea0a033d8f93ed88/src/test/java/io/iworkflow/integ).

Each workflow fixture has its own `Flow<I>` file. All tests run against
`dexcli dev` and cover flows, conditional completion, internal channels, RPCs,
persistence, reset, signals, timers, recovery, and failure behavior.

| Upstream test | Coverage |
| --- | --- |
| `AnyCommandCombinationTest` | condition combinations and WaitFor failure |
| `BasicTest` | start inputs, ID reuse, custom names, config, describe, step wait |
| `ConditionalCompleteTest` | multi-message signal/internal channel draining and conditional close |
| `InternalChannelTest` | parallel movements, payloads, unselected conditions, and channel maps |
| `NoStartStateTest` | no-start, no-step, dead-end, and RPC-triggered movement |
| `PersistenceTest` | typed attributes/maps, indexes, initial values, client reads/writes |
| `ResetTest` | RPC/channel replay counts, AttributeMap locks, and skip-reapply policies |
| `RpcTest` | typed functions, procedures, errors, locking, and channel size |
| `RpcWithMemoTest` | typed RPC persistence without the removed memo API |
| `SignalTest` | typed publish, channel combinations, timer skipping, and closed-flow errors |
| `SkipWaitUntilTest` | execute-only and mixed wait styles |
| `StateOptionsOverrideTest` | per-movement StepOptions overrides |
| `StateOptionsTest` | attribute visibility and parallel WaitFor/Execute locks |
| `StateRecoveryTest` | execute-failure recovery with and without WaitFor |
| `TimerTest` | timer conditions and step-completion waiting |
| `WorkflowUncompletedTest` | timeouts, stop types, user failures, and empty decisions |

`ClientExceptionIntegrationTest` runs against a local gRPC service and verifies
endpoint-aware missing-Flow errors, Worker invocation details, RPC lock
conflicts, long-poll timeouts, and malformed-status fallback behavior.

The dex-dev suite also verifies that read APIs can access closed Flows while
RPC, publish, mutation, and step-wait APIs return `FlowNotActiveException`.

Run the compile check:

```shell
./gradlew compileTestJava
```

Run the E2E suite against `dexcli dev`:

```shell
DEX_SERVER_ADDRESS=127.0.0.1:8801 ./gradlew dexDevTest
```

Indexed persistence cases require `CustomKeywordField`,
`CustomKeywordArrayField`, `CustomStringField`, `CustomDoubleField`,
`CustomIntField`, `CustomBoolField`, and `CustomDatetimeField` in the Temporal
namespace. `run-integration-tests.sh` registers them automatically.

The port does not restore unregistered string/Object APIs. Cross-language names
use explicit Flow, Step, Attribute, and Channel overrides instead.

## Programming-experience findings

- Directly implementing `Flow<I>` and `Step<I>` is substantially clearer than
  assembling callbacks inside public definition builders.
- Typed Attribute, Channel, movement, RPC method-reference, and Client APIs
  catch the important input mismatches during compilation.
- `StepList<StartInput>` binds the Flow input to its optional start Step.
- Flow output is not coupled to `Flow<I>`; callers select an output class in
  `waitForFlow`. A second output type parameter could make this stronger.
- Annotation locks must use durable-name strings because Java annotations
  cannot contain Attribute objects. Registry validation catches those typos.
- The old memo-consistency API does not map forward. Attribute locks provide
  consistency, while BlobCache remains an immutable payload cache.
