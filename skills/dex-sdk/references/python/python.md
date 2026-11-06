# Python

Read this page first for a Python Dex application. Concepts and rules live in
the core files, starting with [Application](../core/application.md). Then
open the Python page for the task:

| Task | Page |
| --- | --- |
| Define Flows, Steps, state, RPCs, waits, Client calls | [primitives](primitives.md) |
| Apply a design pattern | [patterns](patterns.md) |
| Client boundaries, StartFlow, recovery, retries | [error handling](error-handling.md) |
| Real-server tests | [testing](testing.md) |

## Version and API details

Find the installed SDK version before you rely on an API detail:

```bash
python -c 'import importlib.metadata; print(importlib.metadata.version("dex-python-sdk"))'
```

Open the [Python SDK reference](https://docs.superdurable.io/reference/python/)
and select that minor in the left-sidebar version selector. Parameters,
defaults, limits, and version differences live there and in the docstrings of
the installed `dex` package. The installed package wins over this page. Never
invent an API; check the docstrings or the runnable
[Python examples](https://github.com/superdurable/dex/tree/main/examples/python).
The PyPI package is `dex-python-sdk`; it imports as `dex`. Pin an exact
version.

## Choose async or sync

The SDK has two complete surfaces. Pick one per process and match the whole
call chain.

| | Async | Sync |
| --- | --- | --- |
| Client | `AsyncClient` (await every call) | `Client` |
| Worker | `AsyncWorker` (await `start` in a task) | `Worker` (`start` on a thread) |
| Registry | `Registry(flows, allow_async_handlers=True)` | `Registry(flows)` |
| Step handler | `async def execute(self, context: AsyncContext, input: T) -> StepDecision`; plain `def` handlers also run | `def execute(self, context: Context, input: T) -> StepDecision`, or a generator that yields `StepOutput` |
| Heartbeat | `await context.heartbeat(value)` | `yield heartbeat(value)` |
| Stream write | `stream.write(context, value)`, not awaited | `yield stream.write(context, value)` |
| RPC handler | `def` or `async def`, annotated with `Context` | `def`, annotated with `Context` |

- A Registry without `allow_async_handlers=True` rejects every `async def`
  Step, RPC, or timeout handler. Set it whenever any handler is async.
- An async Step annotates its context as `AsyncContext`. Async RPCs and
  timeout handlers keep `Context`.
- Name handler parameters `context` and `input`, as the base classes do;
  pyright rejects a renamed override parameter.
- The `Step` base class declares a sync `execute`, so mypy reports an async
  override. Mark the line `async def execute(  # type: ignore[override]`, as
  the SDK's own type contracts do.
- A sync generator Step is typed `Generator[StepOutput, None, StepDecision]`.
  It must `yield` every heartbeat and Stream output; an output that is created
  but not yielded never reaches Dex. The decision is the generator's `return`
  value, never a yielded item:

  ```python
  def execute(self, context: Context, input: Job) -> Generator[StepOutput, None, StepDecision]:
      for index, item in enumerate(input.items):
          process(item)
          yield heartbeat(index)
      return graceful_complete(len(input.items))
  ```

- An `execute` must not be an async generator; the Registry rejects it.
- Async handlers share one event loop. A blocking call (sync HTTP client,
  `time.sleep`, CPU loop) stalls every handler on the Worker. Use async I/O or
  `asyncio.to_thread`.
- Never call the sync `Client` from an event-loop thread.

## Project shape and wiring

- Construct every Flow instance once, in one composition module. Pass that
  same instance to `Registry` and to Client calls; a new instance of the same
  class is not registered.
- Share one `Registry` and one `open_blob_cache(...)` between the Worker and
  the Client.
- Define every `Attribute`, `Channel`, and `Stream` once at module or class
  scope and return them from `get_persistence_schema`.

```python
import asyncio
from dex import (
    AsyncClient,
    AsyncWorker,
    BlobCacheConfig,
    ClientOptions,
    Flow,
    PullWorkerOptions,
    Registry,
    WorkerOptions,
    open_blob_cache,
)


async def run_application(flows: list[Flow[object]], server_address: str) -> None:
    cache = open_blob_cache(BlobCacheConfig(".dex-blob-cache", 1 << 30))
    registry = Registry(flows, allow_async_handlers=True)
    worker = AsyncWorker(
        registry,
        cache,
        WorkerOptions(
            server_address=server_address,
            pull=PullWorkerOptions(max_concurrent_tasks=8),
        ),
    )
    client = AsyncClient(registry, cache, ClientOptions(server_address=server_address))
    worker_task = asyncio.create_task(worker.start())
    try:
        await worker.wait_pull_registered(timeout=30)
        await serve_http_until_shutdown(client)
    finally:
        await worker.close()
        await worker_task
        await client.close()
        cache.close()


async def serve_http_until_shutdown(client: AsyncClient) -> None:
    await asyncio.Event().wait()
```

The sync surface starts `Worker.start()` on a `threading.Thread`, calls
`worker.wait_pull_registered(timeout=...)` on the main thread, and on shutdown
closes the Worker, joins the thread, closes the Client, and closes the cache.
See the [async composition](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/app.py)
and the [sync composition](https://github.com/superdurable/dex/blob/main/examples/python/sync-python/sync_examples/app.py).

## Worker lifecycle

- `WorkerOptions(server_address=...)` is the only address; the Worker connects
  outbound.
- `start` runs once per Worker and returns only after `stop`. Await its task
  (or join its thread): index and protocol failures surface there.
- Gate readiness with `wait_pull_registered(timeout=...)` before serving
  traffic or retiring the old Worker in a rolling deployment. It raises
  `TimeoutError` on expiry. Probe later readiness with
  `pull_registration_state().is_ready`.
- Always call `stop` or `close` (awaited on the async surface).
- Shut down in this order: stop admitting new requests, close the Worker and
  await its task, then close the Client and the cache. Running handlers may
  still use the Client and the cache while they drain.
- `PullWorkerOptions` sets `max_concurrent_tasks`, `reserved_rpc_tasks`, and
  the drain timeout. See
  [Python Workers](https://docs.superdurable.io/reference/python/workers).

## Authentication and TLS

- `ClientOptions` and `WorkerOptions` each take `credential` and
  `flow_service_tls`. Set them separately on the Client and the Worker.
- `FileCredential` reads a rotating bearer-token file. `ApiKeyCredential`
  exchanges an API key for tokens. A custom `Credential.token()` may return a
  string, or an awaitable for async callers.
- `FlowServiceTlsOptions` turns on TLS; without it the connection is
  plaintext.
- Details: [server authentication](https://docs.superdurable.io/reference/server/authentication).

## Names, types, and payloads

- The default Flow type and Step type is the class name. Name each Flow class
  after its domain, such as `ApprovalFlow`, never just `Flow`. Flow types are
  unique within one Registry; Step types are unique within one Flow. The module
  does not separate two classes with the same name.
- Override `get_flow_type` or `get_step_type` only to keep an identity that is
  already in production after a class rename, or to register several
  instances of one Step class. Each instance then returns a distinct Step type.
- A generic Step whose input annotation is a `TypeVar` overrides
  `get_input_type` to return the concrete type, or `type(None)` for no input.
- Renaming a Python class changes its default Flow or Step type, which open
  runs depend on; see [Flow](../core/flow.md).
- Type every payload concretely: frozen dataclasses, enums, `str`, `int`,
  `list[T]`. Avoid `Any`, lambdas, open handles, and mutable defaults (use
  `field(default_factory=...)`).
- Dataclass field names are the JSON wire names. Add new fields with
  defaults so open runs still decode.
- The JSON codec checks typed dataclass fields and concrete container
  elements recursively, but a few cases are lenient. Inside a dataclass field
  or a container, a union decodes a value that matches no alternative as the
  raw JSON value, a nested `Literal` is not enforced, and a type-variable field
  bound to a primitive type, such as `Result[int]`, keeps its raw JSON value. A `float` field can hold a JSON integer as an
  `int`. Validate those untrusted payload fields in application code.
- When a payload must match another SDK's JSON shape, use dataclass field
  metadata: `dex_json_name` for the wire name, `dex_json_omit_default=True` to
  omit default values, and `dex_json_format="rfc3339"` for Go-compatible
  timestamps. A custom codec goes in the
  `CodecRegistry` passed to `Registry`.
- Use `None` for no payload: `Step[None]`, `Channel("ready", type(None))`.
- An unset Attribute reads as its type's zero value (`0`, `0.0`, `False`) or
  `None`; it does not raise. Model "not set" explicitly when it matters.
- Writing `None` to an Attribute deletes it. A `None` Flow output reads as no
  output; return a result dataclass when that difference matters.

## Connectors

- Use an official Connector only when the catalog lists `python` for it.
- At startup, load the project configuration once with the Connector SDK's
  `load_from_environment`, open each connection by its static name, and pass
  it to the operation's Step factory. Close connections and the project on
  shutdown. Never write your own loader or copy credentials into Flow state.
- Connector Steps are async: run them on `AsyncWorker` with
  `allow_async_handlers=True`. Other rules are in [Connector](../core/connector.md).

## Dex Web metadata

The FDG 2.0 analyzer reads Python source for Step groups, explanations, and
Connector package manifests only. A Python Flow cannot publish Summary,
Display, Action, or Indexed Attribute presentation metadata; do not invent
syntax for it. Put exactly one group line and one explanation line directly
above each local Step class:

```python
# dex:group group-id:lookup group-label:"Lookup"
# dex:explanation text:"Look up a catalog record."
class LookupRecord(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        return graceful_complete(input)
```

Then run `dexcli visualize SOURCE --schema-version 2.0`. Keep Step objects in
the Flow file, keep Step types static, and avoid reflection, `setattr`, and
class decorators other than `dataclass` and `final`. See
[Flow visualization](https://docs.superdurable.io/reference/web/flow-visualization).

## Python gotchas

- `asyncio.sleep` or `time.sleep` in `execute` is not a durable Timer. Put
  business time in a `wait_for` Timer.
- Module globals, `threading.Lock`, `asyncio` tasks, and in-process caches
  vanish on Worker replacement.
- Do not swallow `asyncio.CancelledError` or Dex exceptions to keep going.
- Map instance names are non-empty and contain no `/`.
- A long-poll expiry (`LongPollTimeoutError`) is not a Flow failure.
- Do not use `del` to mark unused parameters; leave them unused.

## Completion checklist

- [ ] The installed SDK version is known, and API shapes match its docstrings.
- [ ] Every reachable Step and every persistence definition is registered.
- [ ] The Registry sets `allow_async_handlers=True` if any handler is async.
- [ ] The surface is consistent: async handlers await I/O; sync generators
      yield every output.
- [ ] Flow and Step types are domain names and unchanged for open runs.
- [ ] The Worker waits for registration with a deadline and is always closed.
- [ ] Client calls have bounded waits and catch typed exceptions.
- [ ] Tests run against a real Dex Server with unique Flow IDs.
