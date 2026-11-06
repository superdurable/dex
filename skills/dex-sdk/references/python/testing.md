# Testing Python applications

Python setup for the strategy in [testing](../core/testing.md). Durable
behavior needs a real Dex Server: mocks cannot prove registration, codecs,
waits, retries, Worker replacement, or terminal semantics. Unit-test only pure
helpers without Dex.

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

- Generate every Flow ID with `uuid4().hex`. Use unique map instances and
  entity keys when tests share an Attribute Store.
- Bound every wait: `wait_for_flow(flow_id, timeout)`, request timeouts on
  Attribute and Step waits, and a deadline loop around `describe_flow` only
  when no Client wait exists. Never `await asyncio.sleep(...)` as the
  assertion.
- Skip the suite with a clear message when no server answers
  `health_check()`, instead of failing every test.
- On a timeout, print the Flow ID, run ID, status, and the active Step before
  failing.
- Assert business invariants, not the scheduling order of parallel Steps.
- `dexcli dev` defaults to strict durability, which is slow; a short-lived
  test server can relax it. See [testing](../core/testing.md).

## Crash a Worker

`stop()` drains running handlers, so it cannot simulate a crash during
`execute`. Run the Worker in a subprocess (`asyncio.create_subprocess_exec`),
wait until the Flow reaches the boundary under test, send `SIGKILL`, start a
replacement Worker with the same registered definitions, and assert that the
Flow resumes and completes without a duplicate external effect.

## Scenarios

1. Start, wait, and assert the output and terminal status.
2. Replace the Worker after a durable boundary and prove the Flow resumes.
3. Exhaust Step retries and assert the failure route or the Flow failure.
4. Publish through RPCs before and during the wait; assert consumption,
   deletion, and RPC idempotency.
5. Cross a Timer without sleeping in the test.
6. Read Stream messages with resume tokens.
7. Cover graceful and force completion, failure, cancellation, the timeout
   handler, and a dead end separately.
8. StartFlow retry identity: same, different, and generated request IDs,
   with `ignore_already_started` on and off, and a lost start response.
9. For each entity readable after close, close the real Flow, then read it
   through the application's Get boundary.
10. Run both the async and the sync surface when the product ships both.

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
