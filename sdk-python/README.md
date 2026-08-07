
# Dex SDK for Python

Python SDK for [Dex workflow engine](https://github.com/superdurable/dex)

## New user contracts

The rewrite targets Python 3.11+ and exposes strongly typed workflow contracts
from `dex`. This phase includes definitions, attributes, channels, waits,
decisions, codecs, registry validation, synchronous client calls, and synchronous
or asynchronous worker handlers. Python owns its gRPC Client and Worker transport;
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
        return dex.Wait.all_of(
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
Built-in scalar types and dataclasses need no codec arguments. Register an
explicit codec only for a custom encoding or a type Registry cannot derive.
`PersistenceSchema.of(...)` accepts attributes and channels together and
partitions them by definition type.

Initial attributes retain their value types without a public wrapper class:

```python
options = (
    dex.StartFlowOptions()
    .with_attribute(counter, 1)
    .with_attribute(counters_by_region, "us-west", 1)
)
```

```
pip install dex-python-sdk==0.0.1
```

See [samples](../examples/python) for use case examples.

## Requirements

- Python 3.11+
- [Dex server](https://github.com/superdurable/dex#how-to-use)

## Concepts

Applications implement two generic interfaces from [`dex.contracts`](dex/contracts/):

- `Flow[START_INPUT]` returns `StepList.start_step(...)`, followed by optional
  `.other_steps(...)`, from one `get_steps()` method. The `StepList` generic
  binds the Flow input to the starting Step input. Use `StepList.empty()` when
  a Flow has no Steps.
- `Step[INPUT]` implements `execute` and optionally `wait_for`; either handler
  may be synchronous or asynchronous.

`StepOptions.wait_for_method_timeout` and `execute_method_timeout` bound the
two handler calls. Timer and channel conditions determine how long a Step waits.

`Registry` validates every Flow, Step, RPC signature, durable name, lock, and
codec before Client or Worker startup. `Client` methods use these typed objects
instead of raw Flow, Step, or RPC strings.

The complete legacy IWF integration inventory has a compile-only port under
[`tests/iwfcompat`](tests/iwfcompat/README.md). Its 28 Flow fixtures and 16
scenario files show the Python programming model without starting a server.

## Implementation status

The new public contracts and validation are implemented. Client and Worker
transport will use Python gRPC directly. The native bridge is limited to the
shared Rust BlobCache.

## Running dex-server locally

### Option 1: use docker compose
See [dex README](https://github.com/superdurable/dex#using-docker-image--docker-compose)

### Option 2: VSCode Dev Container

Dev Container is an easy way to get dex-server running locally. Follow these steps to launch a dev container:
- Install Docker, VSCode, and [VSCode Dev Container plugin](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers).
- Open the project in VSCode.
    ```bash
    cd dex-python-sdk
    code .
    ```
- Launch the Remote-Containers: Reopen in Container command from Command Palette (Ctrl + Shift + P). You can also click in the bottom left corner to access the remote container menu.
- Once the dev container starts, dex-server will be listening on port 8801.

## How To Contribute

This project uses [uv](https://docs.astral.sh/uv/) for Python versions,
dependencies, virtual environments, locking, building, and publishing.

To install requirements:

```bash
uv sync --locked
```

#### Update IDL

Edit [`protos/dex.proto`](../protos/dex.proto). Rename catalog: [`docs/design/idl-renames.md`](../docs/design/idl-renames.md).

#### Generate stubs from IDL

```bash
make -C ../protos proto-python
```

Checked-in Python stubs land in `dex/dexpb/`.
#### Linting

To run linting for this project:

```bash
uv run --frozen pre-commit run --show-diff-on-failure --color=always --all-files
```

## Code of Conduct
This project is governed by the [Contributor Covenant v 1.4.1](CODE_OF_CONDUCT.md). (Review the Code of Conduct and remove this sentence before publishing your project.)

## Publishing to PyPI

1. Bump `version` in `pyproject.toml` (and the `pip install` line above).
2. Create a GitHub Release with tag `sdk-python-vX.Y.Z` (for example `sdk-python-v0.0.1`), or run the **Publish Python SDK to PyPI** workflow manually.
3. CI runs `uv build --no-sources` and `uv publish` using the `PYPI_TOKEN` repository secret.

See [CONTRIBUTING.md](../CONTRIBUTING.md#releases-monorepo-tags) for monorepo tag conventions.

## License

[Super Durable Source License 1.0](LICENSE), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
