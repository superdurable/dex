# Rust IWF compatibility compile contracts

These modules translate the Java IWF compatibility workflows and client call
sites into idiomatic, strongly typed Rust. They compile but are not executed
until the Rust Client and Worker runtime are implemented.

| Java test | Rust module |
| --- | --- |
| `AnyCommandCombinationTest` | `channels.rs` |
| `BasicTest` | `basic.rs` |
| `ConditionalCompleteTest` | `channels.rs` |
| `InternalChannelTest` | `channels.rs` |
| `NoStartStateTest` | `operations.rs`, `rpc.rs` |
| `PersistenceTest` | `persistence.rs` |
| `ResetTest` | `operations.rs` |
| `RpcTest`, `RpcWithMemoTest` | `rpc.rs` |
| `SignalTest`, `TimerTest` | `channels.rs` |
| `SkipWaitUntilTest` | `basic.rs` |
| `StateOptionsTest`, `StateOptionsOverrideTest` | `state.rs` |
| `StateRecoveryTest` | `state.rs` |
| `WorkflowUncompletedTest` | `state.rs`, `operations.rs` |

The source Java scenarios remain the behavioral authority until Rust E2E tests
run against `dexcli dev`.
