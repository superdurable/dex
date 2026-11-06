# Python

Use this page first for a Python Dex application. It covers project shape,
registration, the async and sync surfaces, Worker lifecycle, and Python traps.
Concepts and rules live in core: [Flow](../core/flow.md), [Step](../core/step.md),
[Attribute](../core/attribute.md), [RPC](../core/rpc.md),
[Channel](../core/channel.md), [Stream](../core/stream.md),
[Timer](../core/timer.md), [SubFlow](../core/subflow.md),
[Client](../core/client.md), [Application](../core/application.md),
[Connector](../core/connector.md), [error handling](../core/error-handling.md),
[testing](../core/testing.md), and [patterns](../core/patterns.md).

Then open the Python page for the task:

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
- A sync generator Step must `yield` every heartbeat and Stream output. An
  output that is created but not yielded never reaches Dex.
- Async handlers share one event loop. A blocking call (sync HTTP client,
  `time.sleep`, CPU loop) stalls every handler on the Worker. Use async I/O or
  `asyncio.to_thread`.
- Never call the sync `Client` from an event-loop thread.

## Project shape and wiring

- Keep Flow and Step classes in application modules. Keep HTTP handlers and
  Client adapters outside Flow classes.
- Construct every Flow instance once, in one composition module. Pass that
  same instance to the Registry and to Client calls; a new instance of the same
  class is not registered.
- Create one Registry and one BlobCache per process. Share both between the
  Worker and the Client.
- Inject business services through Flow and Step constructors.
- Define every Attribute, Channel, and Stream once at module or class scope.
  Return them from `get_persistence_schema`.

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
        await client.close()
        await worker.close()
        await worker_task
        cache.close()


async def serve_http_until_shutdown(client: AsyncClient) -> None:
    await asyncio.Event().wait()
```

The sync surface starts `Worker.start()` on a `threading.Thread`, calls
`worker.wait_pull_registered(timeout=...)` on the main thread, and on shutdown
closes the Client, closes the Worker, joins the thread, and closes the cache.
See the [async composition](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/app.py)
and the [sync composition](https://github.com/superdurable/dex/blob/main/examples/python/sync-python/sync_examples/app.py).

## Worker lifecycle

- Every Worker is a pull Worker. It needs only an outbound connection to the
  Dex Server. There is no Worker address, bind port, or routing setting.
- `start` negotiates the protocol, synchronizes Attribute indexes, opens the
  task streams, and returns only after `stop`. It may run once per Worker.
  Always observe its result: an index or protocol failure surfaces there.
- Gate readiness with `wait_pull_registered(timeout=...)` before serving
  traffic or retiring the old Worker in a rolling deployment. It raises
  `TimeoutError` on expiry. Probe later readiness with
  `pull_registration_state().is_ready`.
- `stop` and `close` drain running handlers, then cancel the rest. A task that
  arrives during shutdown returns to Dex without running. Always call one of
  them, and await it on the async surface.
- `max_concurrent_tasks` is the number of execution slots. Some slots can be
  reserved for RPCs so RPCs stay responsive while Steps fill the Worker. Steps
  are never declined for capacity. See
  [Python Workers](https://docs.superdurable.io/reference/python/workers) for the
  option names and defaults.
- A Worker serves only the Flow types in its Registry. Every Worker of one
  deployment must register compatible definitions.

## Authentication and TLS

- `ClientOptions` and `WorkerOptions` each take `credential` and
  `flow_service_tls`. Set them separately on the Client and the Worker.
- `FileCredential` reads a rotating bearer-token file. `ApiKeyCredential`
  exchanges an API key for tokens. A custom `Credential.token()` may return a
  string, or an awaitable for async callers.
- `FlowServiceTlsOptions` turns on TLS. Without it the connection is
  plaintext; protect it at the network layer.
- Keep tokens and keys out of Flow input, Attributes, and logs.
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
- Flow type, Step types, and persistence names are durable identity. Open runs
  resume on new code, so never rename them as a refactor. For an incompatible
  change, add a new Flow or Step type and keep the old one registered until its
  runs drain.
- Type every payload concretely: frozen dataclasses, enums, `str`, `int`,
  `list[T]`. Avoid `Any`, lambdas, open handles, and mutable defaults (use
  `field(default_factory=...)`).
- Field names and types of durable payloads are a wire contract. Add fields
  with defaults; never repurpose or remove one that open runs still read.
- When a payload must match another SDK's JSON shape, use dataclass field
  metadata such as `dex_json_name`. A custom codec goes in the
  `CodecRegistry` passed to `Registry`.
- Use `None` for no payload: `Step[None]`, `Channel("ready", type(None))`.
- An unset Attribute reads as its type's zero value (`0`, `0.0`, `False`) or
  `None`; it does not raise. Model "not set" explicitly when it matters.
- Writing `None` to an Attribute deletes it. A `None` Flow output is
  indistinguishable from no output; return a result dataclass when that
  difference matters.

## Connectors

- Use an official Connector only when the catalog lists `python` for the
  connector and for every operation and Trigger you need. Install the PyPI
  version the catalog gives for Python.
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
- Process globals, threads, tasks, locks, and caches vanish on Worker
  replacement. Durable coordination uses Attributes, Channels, and locks.
- Do not swallow `asyncio.CancelledError` or Dex exceptions to keep going.
- Every Context write commits only when the handler returns successfully.
  Nothing is visible outside the handler before that.
- Map instance names are non-empty and contain no `/`.
- A long-poll expiry (`LongPollTimeoutError`) is not a Flow failure.
- Graceful and force completion differ for running branches; see
  [Step](../core/step.md).
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
- [ ] External effects are idempotent, keyed by durable identity.
- [ ] Tests run against a real Dex Server with unique Flow IDs.
