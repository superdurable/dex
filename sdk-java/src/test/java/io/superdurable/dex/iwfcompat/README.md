# IWF Java integration port

This directory ports the complete integration scenario inventory from
[`indeedeng/iwf-java-sdk`](https://github.com/indeedeng/iwf-java-sdk/tree/8fa04457c0abcc4473300f17ea0a033d8f93ed88/src/test/java/io/iworkflow/integ).
That snapshot contains 16 top-level integration tests and 65 workflow/support
files.

Each workflow fixture has its own `Flow<I>` file. The suite currently has 11
real E2E cases covering Basic, conditional completion, internal channels, RPC
locking, and persistence. The remaining upstream scenarios continue to compile
while their original assertions are ported.

| Upstream test | Compile-port coverage |
| --- | --- |
| `AnyCommandCombinationTest` | condition combinations and WaitFor failure |
| `BasicTest` | start inputs, ID reuse, custom names, config, describe, step wait |
| `ConditionalCompleteTest` | typed signal/internal channels and conditional close |
| `InternalChannelTest` | parallel movements, channel maps, batched publish |
| `NoStartStateTest` | no-start, no-step, dead-end, and RPC-triggered movement |
| `PersistenceTest` | typed attributes/maps, indexes, initial values, client reads/writes |
| `ResetTest` | locking-RPC and channel-message reapply policies |
| `RpcTest` | typed functions, procedures, errors, locking, and channel size |
| `RpcWithMemoTest` | typed RPC persistence without the removed memo API |
| `SignalTest` | typed publish, channel combinations, and timer skipping |
| `SkipWaitUntilTest` | execute-only and mixed wait styles |
| `StateOptionsOverrideTest` | per-movement StepOptions overrides |
| `StateOptionsTest` | timeout, retry, durability, and attribute locks |
| `StateRecoveryTest` | execute-failure recovery with and without WaitFor |
| `TimerTest` | timer conditions and step-completion waiting |
| `WorkflowUncompletedTest` | timeouts, stop types, user failures, and empty decisions |

Run the compile check:

```shell
./gradlew compileTestJava
```

Run the enabled E2E cases against `dexcli dev`:

```shell
DEX_SERVER_ADDRESS=127.0.0.1:8801 ./gradlew dexDevTest
```

Indexed persistence cases require `CustomKeywordField`, `CustomIntField`, and
`CustomDatetimeField` in the Temporal namespace. See the SDK README for the
setup commands.

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
