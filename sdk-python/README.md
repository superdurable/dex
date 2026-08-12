
# Dex SDK for Python

Python SDK for [Dex workflow engine](https://github.com/superdurable/dex)

## New user contracts

The rewrite targets Python 3.11+ and exposes strongly typed workflow contracts
from `dex`. This phase includes definitions, attributes, channels, waits,
decisions, codecs, registry validation, synchronous client calls, and synchronous
worker handlers. Python owns its gRPC Client and Worker transport;
the shared Rust Core is used only for BlobCache.

```python
from datetime import timedelta

import dex

counter = dex.Attribute("counter", int)
counters_by_region = dex.AttributeMap("counters-by-region", int)

class Run(dex.Step[str]):
    def wait_for(
        self, context: dex.Context, input: str
    ) -> dex.Wait:
        return dex.Wait.until(
            dex.Timer.by_duration(timedelta(seconds=1))
        )

    def execute(
        self, context: dex.Context, input: str
    ) -> dex.StepDecision:
        return dex.graceful_complete(input)

class CounterFlow(dex.Flow[str]):
    run = Run()

    def get_flow_type(self) -> str:
        return "Counter"

    def get_steps(self) -> dex.StepList[str]:
        return dex.StepList.start_step(self.run)

    def get_persistence_schema(self) -> dex.PersistenceSchema:
        return dex.PersistenceSchema.of(counter, counters_by_region)

    @dex.rpc(name="Increment")
    def increment(
        self, context: dex.Context, input: int
    ) -> dex.RPCResult[int]:
        return dex.RPCResult(input + 1)

flow = CounterFlow()
registry = dex.Registry((flow,))
```

Registry derives codecs from declared Python types and handler annotations.
Built-in primitive types and dataclasses need no codec arguments. Register an
explicit codec only for a custom encoding or a type Registry cannot derive.
`PersistenceSchema.of(...)` accepts attributes and channels together and
partitions them by definition type.

`Worker` and `AsyncWorker` synchronize all registered Indexed Attributes with
Dex Server before opening their listener. Existing indexes return immediately;
failure or the default two-minute deadline aborts startup. An indexed
`AttributeMap` must provide one fixed `index_key`.

Initial attributes retain their value types without a public wrapper class:

```python
options = (
    dex.StartFlowOptions()
    .with_attribute(counter, 1)
    .with_attribute(counters_by_region, "us-west", 1)
)
```

Opt in when declaring an Attribute or AttributeMap, and select the Store in
Flow configuration:

```python
email = dex.Attribute("customer-email", str, sync_to_attribute_store=True)
config = dex.FlowConfig(attribute_store_name="profiles")
```

The Store is an asynchronous latest-state projection. Deletion writes SQL
`NULL`, and projection failures do not roll back Flow Attributes. `None`
preserves the current target; an explicit empty string disables future
synchronization while retaining protocol presence.

```
pip install dex-python-sdk==0.1.0
```

See [samples](../examples/python) for use case examples.

## Requirements

- Python 3.11+
- [Dex server](https://github.com/superdurable/dex#how-to-use)

## Concepts

Applications implement two generic interfaces from [`dex`](dex/):

- `Flow[START_INPUT]` returns `StepList.start_step(...)`, followed by optional
  `.other_steps(...)`, from one `get_steps()` method. The `StepList` generic
  binds the Flow input to the starting Step input. Use `StepList.empty()` when
  a Flow has no Steps.
- `Step[INPUT]` implements `execute` and optionally `wait_for`. The default
  Worker path requires synchronous handlers. With `AsyncWorker` and
  `Registry(..., allow_async_handlers=True)`, handlers may be `async def` and
  `await` an `AsyncClient`.

`StepOptions.wait_for_method_timeout` and `execute_method_timeout` bound the
two handler calls. Timer and channel conditions determine how long a Step waits.

`Registry` validates every Flow, Step, RPC signature, durable name, lock, and
codec before Client or Worker startup. `Client` methods use these typed objects
instead of raw Flow, Step, or RPC strings.

### Waiting and map inspection

`Wait.all_of` and `Wait.any_of` may use unnamed Conditions. Every Condition in
`Wait.any_combination_of` must have a non-empty user ID; the same Condition
instance may appear in multiple combinations.

Both `Client` and `AsyncClient` provide singleton and AttributeMap-instance
overloads of `wait_for_attribute_equal`. They target the current run and accept
only string, bool, int, or float wire values. JSON objects, bytes, and null fail
before transport. `AttributeMap.get_map_size/get_all_instance_keys` include
buffered sets and deletes. The matching `ChannelMap` methods are RPC-only,
include buffered publishes, and omit empty instances. Keys are decoded and
sorted. Use `force_complete_if_channels_empty(...)` for conditional completion.

`Client.wait_for_flow` and `AsyncClient.wait_for_flow` return a
`WaitForFlowResult` after hydrating every output-bearing completion. Use
`single_output` only when the Flow contract produces exactly one output:

```python
output = client.wait_for_flow(flow_id).single_output(OrderResult)

result = client.wait_for_flow(flow_id)
for completion in result.completions:
    if completion.step_execution_id == expected_execution_id:
        output = completion.decode(OrderResult)
```

`completions` is an immutable tuple in server collection order. Parallel branch
order is not deterministic, so select by `step_type` or `step_execution_id`.
No-output Flows return an empty tuple; `single_output` raises `ValueError` for
zero or multiple completions. `FlowUncompletedError.completions` uses the same
hydrated model for partial outputs.

### Errors

Client calls raise concrete `DexServiceError` subclasses. Existing-Flow reads
(`get_attribute`, `describe_flow`, `wait_for_flow`, and `reset_flow`) raise
`FlowNotFoundError` when the Flow does not exist. Mutations, RPCs, timer/Step
waits, config updates, and continue-as-new triggers raise
`FlowNotActiveError` when no running Flow can accept the operation.

```python
try:
    client.publish(flow_id, orders.approved, order_id)
except dex.FlowNotActiveError:
    # The Flow is missing or already closed.
    pass
```

Duplicate starts, worker failures, RPC lock contention, and long-poll timeouts
raise `FlowAlreadyStartedError`, `WorkerInvocationError`,
`RpcLockConflictError`, and `LongPollTimeoutError`. All service errors retain
`code`, `sub_status`, `detail`, `operation`, `flow_id`, and the original gRPC
exception through Python exception chaining. Worker failures also expose
`worker_code`, `worker_error_type`, and `worker_error_detail`. Registration,
serialization, and invalid handler returns use `FlowDefinitionError`,
`ValueMappingError`, and `InvalidStepResultError`.

### Sync vs asyncio

- **Sync (default):** `Client` and `Worker` use blocking gRPC and a thread-pool
  Worker. Blocking `Client` calls inside `Step.execute` are safe (one pool
  thread is occupied; other RPCs still run).
- **Asyncio:** `AsyncClient` and `AsyncWorker` use `grpc.aio`. Use
  `Registry(..., allow_async_handlers=True)` when Steps/RPCs are coroutines.
  Inside async `execute`, inject `AsyncClient` — do not call sync `Client` on
  the Worker event loop. Sync `Worker` still rejects coroutine handlers at
  registry construction unless `allow_async_handlers=True` (and even then the
  sync Worker dispatcher rejects awaitable return values).

Integration scenarios live under
[`tests/integ`](tests/integ/README.md). They exercise the same workflows,
client operations, and assertions as the Java suite against an isolated
`dexcli dev` environment.

## Implementation status

The strongly typed contracts, registry, synchronous Client/Worker, optional
`AsyncClient`/`AsyncWorker` (`grpc.aio`), and Rust-backed BlobCache are
implemented. Python owns its gRPC transport; the native bridge is limited to
the shared BlobCache. Design notes:
[`docs/design/plan/python-sdk-async-apis.md`](../docs/design/plan/python-sdk-async-apis.md).

## Running Dex locally

Install and start the complete local environment with `dexcli`:

```bash
brew install superdurable/tap/dexcli
dexcli dev
```

Dex Server listens on `127.0.0.1:8801`. See the
[CLI README](../cli/README.md) for endpoints and persistence options.

## How To Contribute

This project uses [uv](https://docs.astral.sh/uv/) for Python versions,
dependencies, virtual environments, locking, building, and publishing.

To install requirements:

```bash
uv sync --locked
```

Run the complete Python SDK integration suite with an isolated Dex development
environment:

```bash
./run-integration-tests.sh
```

### Measure integration coverage

Run the same integration suite with Python source coverage:

```bash
./run-integration-tests.sh --coverage
```

Only the integration scenarios contribute execution data, and only production
Python modules under `dex` are measured. Generated protobuf modules under
`dex/dexpb` are excluded. The
terminal report lists uncovered line ranges. The browser report starts at
`coverage/html/index.html`; `coverage/coverage.xml` and `coverage/lcov.info`
are also generated.

CI uploads LCOV to Codecov with GitHub OIDC under the
`sdk-python-integration` flag and retains the full report as the
`sdk-python-integration-coverage` Actions artifact.

#### Update IDL

Edit [`protos/dex.proto`](../protos/dex.proto). Rename catalog: [`docs/design/idl-renames.md`](../docs/design/idl-renames.md).

#### Generate stubs from IDL

```bash
make -C ../protos proto-python
```

Checked-in Python stubs land in `dex/dexpb/`.
#### Linting

Validate that every `dex.__all__` class, function, constant, public method,
argument, return value, dataclass field, enum value, and public instance
attribute has a Google-style docstring:

```bash
uv run --frozen python scripts/check_public_docs.py
```

The checker resolves definitions from the public package export table, so
private helpers and generated protobuf modules are excluded. Use `help(dex.Client)`
or IDE hover information to read the same documentation. To run all other
linting for this project:

```bash
uv run --frozen pre-commit run --show-diff-on-failure --color=always --all-files
```

## Code of Conduct
This project is governed by the [Contributor Covenant v 1.4.1](CODE_OF_CONDUCT.md). (Review the Code of Conduct and remove this sentence before publishing your project.)

## Publishing to PyPI

1. Optionally run **Publish Python SDK to PyPI** via workflow_dispatch with a version and
   `publish=false` to validate all distributions without uploading.
2. Create a GitHub Release with tag `sdk-python/vX.Y.Z` (for example `sdk-python/v0.1.0`).
   CI stamps that version into `pyproject.toml` for the build (same idea as the TypeScript
   SDK release), then builds and smoke-tests Linux x86_64/ARM64, macOS x86_64/ARM64, and
   Windows x86_64 wheels, verifies the source distribution, and publishes with `PYPI_TOKEN`.
3. After publishing, bump the committed `pyproject.toml` / docs install line when you want
   the repo tip to reflect the released version.

A manual run publishes only from `main`, and only when `publish` is explicitly selected.
The dispatch `version` input is stamped the same way as a release tag.

See [CONTRIBUTING.md](../CONTRIBUTING.md#releases-monorepo-tags) for monorepo tag conventions.

## License

[Super Durable Source License 1.0](LICENSE), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
