# Rust IWF compatibility integration tests

These modules translate the Java IWF compatibility workflows and client call
sites into idiomatic, strongly typed Rust. Every Java integration test has a
corresponding Rust E2E test with the same workflow behavior and assertions.
Go-only runtime scenarios for handler metadata, `wait_for` failures and timeouts,
and locked RPC publication are covered too.

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

Run all scenarios against an isolated `dexcli dev` stack from `sdk-rust`:

```bash
./run-integration-tests.sh
```

Go's erased `ChannelDef` variant has no Rust counterpart: Rust keeps typed
`Channel<T>` values through registration, so the runtime channel scenario does
not need a second type-erased test. Go's pointer/value Flow cases likewise map
to one Rust ownership model and share one runtime scenario.
