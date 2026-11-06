# Testing Python applications

Python setup for the strategy and scenarios in [testing](../core/testing.md).

## Harness

Run `dexcli dev` for a local server. Start the application's Worker and Client
from the same Registry and BlobCache, once per test session, and close them in
the fixture.

```python
import asyncio
from collections.abc import AsyncIterator
from datetime import timedelta
from pathlib import Path
from uuid import uuid4

import pytest
import pytest_asyncio

from dex import (
    AsyncClient, AsyncWorker, BlobCacheConfig, ClientOptions, Context, Flow,
    FlowStatus, Registry, Step, StepDecision, StepList, WorkerOptions,
    graceful_complete, open_blob_cache,
)

SERVER_ADDRESS = "127.0.0.1:8801"
WAIT_TIMEOUT = timedelta(seconds=45)


class Greet(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        return graceful_complete(f"Hello, {input}!")


class GreetingFlow(Flow[str]):
    def __init__(self) -> None:
        self.greet = Greet()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.greet)


GREETING_FLOW = GreetingFlow()


def new_flow_id(prefix: str) -> str:
    return f"{prefix}-{uuid4().hex}"


@pytest_asyncio.fixture(scope="session")
async def client(tmp_path_factory: pytest.TempPathFactory) -> AsyncIterator[AsyncClient]:
    cache_dir: Path = tmp_path_factory.mktemp("dex-blob-cache")
    cache = open_blob_cache(BlobCacheConfig(str(cache_dir), 1 << 28))
    registry = Registry([GREETING_FLOW], allow_async_handlers=True)
    worker = AsyncWorker(registry, cache, WorkerOptions(server_address=SERVER_ADDRESS))
    dex_client = AsyncClient(registry, cache, ClientOptions(server_address=SERVER_ADDRESS))
    worker_task = asyncio.create_task(worker.start())
    try:
        await worker.wait_pull_registered(timeout=30)
        yield dex_client
    finally:
        await dex_client.close()
        await worker.close()
        await worker_task
        cache.close()


@pytest.mark.asyncio
async def test_greeting_completes(client: AsyncClient) -> None:
    flow_id = new_flow_id("greeting")
    await client.start_flow(GREETING_FLOW, flow_id, "Ada")
    result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.status is FlowStatus.COMPLETED
    assert result.single_output(str) == "Hello, Ada!"
```

Client calls take the registered Flow instance. In a real suite, import it
from the application's composition module.

- Generate every Flow ID with `uuid4().hex`.
- Bound every wait: `wait_for_flow(flow_id, timeout)`, `request_timeout` on
  Attribute and Step waits. Never `await asyncio.sleep(...)` as the assertion.
- Skip the suite with a clear message when `health_check()` fails.
- Mark async tests with `pytest.mark.asyncio` and use `pytest_asyncio`
  fixtures; a sync suite uses `Client` and a Worker thread.

## Crash a Worker

`stop()` drains running handlers, so it cannot simulate a crash. Run the
Worker with `asyncio.create_subprocess_exec`, send `SIGKILL` at the boundary
under test, and start a replacement Worker with the same definitions.

## Scenarios

Run the core scenarios. When the product ships both surfaces, run them on the
async and the sync surface.

## Commands

```bash
dexcli dev
cd examples/python
uv sync --locked
make unitTests
make e2eTests
make syncIntegTests
```

The [example integration harness](https://github.com/superdurable/dex/blob/main/examples/python/tests/integ/conftest.py)
shows a session-scoped application fixture, the server probe, and bounded
polling helpers.
