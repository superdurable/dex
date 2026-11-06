
# Dex SDK for Python

## Authentication

When Dex authentication is enabled, pass a bearer credential to each Client and
Worker that connects to the server. FileCredential reads the token at construction,
checks its mtime and size at most once per second, and reloads it after rotation.
Whitespace around the token is removed. An unreadable or empty file fails calls
until it is restored; the SDK does not send an old token.

Use `ApiKeyCredential` when the application holds a long-lived API key and a
token endpoint issues short-lived access tokens scoped to a server ID. Supply
the token URL, server ID, and API key explicitly; the server ID is never derived
from the endpoint host. The endpoint must use HTTPS, except loopback HTTP on
`localhost`, `127.0.0.0/8`, or `::1` for local development.
Loopback HTTP bypasses environment proxies so the API key stays on the host.
HTTPS uses configured proxies with TLS through CONNECT. Token endpoint redirects
are refused, including redirects that preserve the POST method.

```python
from dex import ApiKeyCredential, Client, ClientOptions

credential = ApiKeyCredential(
    "https://auth.example.com/token", "dex-server", "application-api-key"
)
client = Client(registry, cache, ClientOptions(credential=credential))
try:
    client.health_check()
finally:
    client.close()
    credential.close()
```

The first call exchanges the key for a token. After 80% of its lifetime, a
single background refresh begins while calls continue using the valid token.
Callers waiting for an expired token share the exchange. Failed refreshes retry
no more often than every five seconds while the previous token remains valid.
Each exchange has a 10-second deadline by default, configurable through
`exchange_timeout`. It covers connecting, sending, reading, and parsing. A Dex
`UNAUTHENTICATED` response discards the rejected token
and retries a unary call once with a fresh token. Pull Worker streams reconnect
once immediately with a fresh token; a second rejection uses the normal backoff.
The credential is thread-safe.
It redacts the API key, issued access tokens, and JWT-shaped strings from
exchange errors, including endpoint details and transport causes. Client
calls raise `ApiKeyExchangeError` unchanged; pull Workers log it and retry.
Closing the credential is optional. After all Clients and Workers sharing it
are closed, `close()` prevents further token calls and waits at most until the
current exchange deadline. An uninterruptible network operation may continue
in its daemon thread after that deadline.

```python
from dex import Client, ClientOptions, FileCredential, Worker, WorkerOptions

credential = FileCredential("/var/run/secrets/dex/token")
client = Client(registry, cache, ClientOptions(credential=credential))
worker = Worker(registry, cache, WorkerOptions(credential=credential))
```

The same option works with AsyncClient and AsyncWorker. Worker credentials cover
startup synchronization, blob hydration, and pull WorkerTaskService streams. A
pull stream reads the token when it opens and again on every reconnect. For a
custom credential, implement Credential.token() to return the current token;
the method may be called concurrently, and an exception fails the call before
it contacts Dex. Client and AsyncClient calls raise the credential's exception
unchanged, including TimeoutError when the RPC deadline has not expired.
AsyncClient and AsyncWorker also accept an awaitable token. Synchronous tokens
run in a dedicated four-thread executor per async Client or Worker, created
lazily and shut down without waiting for a blocked token call on close. Sync
calls run token lookup on the calling thread. Async calls bound token lookup
by the RPC deadline when set; pull stream opens use a 30-second lookup budget.
Declare token with `async def` to invoke it on the event loop. A plain token
function runs once in the SDK executor; if it returns a coroutine, that
coroutine runs on the event loop. A loop-bound Future from another loop raises
TypeError; use `async def` to create it on the right loop. A blocked token keeps
one executor thread busy until it returns, and an unclosed Client or Worker may
retain up to four idle credential threads. Sync token lookup must return
promptly. By default, no authorization metadata is sent. The connection uses
plaintext gRPC unless `flow_service_tls` is set; protect the network path that
carries the token.

## TLS

Set `ClientOptions.flow_service_tls` and `WorkerOptions.flow_service_tls`
independently for a TLS endpoint, such as a TLS gateway in front of Dex.
Omitting the option keeps plaintext gRPC for local `dexcli dev` servers.
`FlowServiceTlsOptions()` enables TLS 1.2 or newer, verifies the certificate
and hostname, and requires HTTP/2 ALPN `h2`. Workers apply it to startup,
blob hydration, and pull task streams. The same settings work with `AsyncClient`
and `AsyncWorker`. File, API key, and custom bearer credentials compose with TLS
as call metadata.

With an application's registered `flow`, `registry`, and open `cache`:

```python
from concurrent.futures import ThreadPoolExecutor
from datetime import timedelta
from pathlib import Path
from uuid import uuid4

from dex import (
    Client, ClientOptions, FileCredential, FlowServiceTlsOptions,
    Worker, WorkerOptions,
)

flow_service_tls = FlowServiceTlsOptions(
    additional_root_cas_pem=Path("/etc/dex/root-ca.pem").read_bytes(),
)
credential = FileCredential("/var/run/secrets/dex/token")
server_address = "dex.example.com:443"
with Worker(registry, cache, WorkerOptions(
    server_address=server_address,
    flow_service_tls=flow_service_tls,
    credential=credential,
)) as worker, Client(registry, cache, ClientOptions(
    server_address=server_address,
    flow_service_tls=flow_service_tls,
    credential=credential,
)) as client, ThreadPoolExecutor(max_workers=1) as executor:
    started = executor.submit(worker.start)
    try:
        worker.wait_pull_registered(timeout=30)
        flow_id = f"tls-{uuid4()}"
        client.start_flow(flow, flow_id, input_value)
        result = client.wait_for_flow(flow_id, timedelta(seconds=30))
    finally:
        worker.stop()
        started.result(timeout=10)
```

- `additional_root_cas_pem` accepts PEM bytes or text. Omit it, or use an empty
  value, for gRPC's default trust. A nonempty bundle must contain at least one
  parsable certificate; malformed and non-certificate blocks are skipped when
  another certificate parses. The trust is the gRPC default roots (grpcio's
  bundled roots, or `GRPC_DEFAULT_SSL_ROOTS_FILE_PATH` when set) plus the added
  roots. A configured gRPC root file replaces the bundled roots and takes
  precedence over system roots. Adding roots fails when
  `GRPC_USE_SYSTEM_ROOTS_OVER_LANGUAGE_CALLBACK` is true and
  `GRPC_NOT_USE_SYSTEM_SSL_ROOTS` is not true, unless a root file is configured.
  True values are `true`, `t`, `yes`, `y`, and `1`, ignoring case. This guard
  also applies to system roots selected with `GRPC_SYSTEM_SSL_ROOTS_DIR`.
  To add roots in that configuration, disable
  `GRPC_USE_SYSTEM_ROOTS_OVER_LANGUAGE_CALLBACK` and pass the full PEM bundle
  as `additional_root_cas_pem`, or set `GRPC_DEFAULT_SSL_ROOTS_FILE_PATH` to the
  full bundle. `SSL_CERT_FILE` and `SSL_CERT_DIR` do not affect this trust.
  These sources are read at construction. An unreadable bundled root file, or an
  unreadable or invalid configured gRPC root file, fails construction when adding
  roots. Errors for configured files include the file path. To trust OS or
  corporate CAs, pass their PEM bundle explicitly:

  ```python
  tls = FlowServiceTlsOptions(
      additional_root_cas_pem=Path("/etc/ssl/certs/ca-certificates.crt").read_bytes(),
  )
  ```

  Use the CA bundle path supplied by your OS or organization.
- `server_name` defaults to `None`, which verifies the address host and preserves
  the host and port as HTTP/2 authority. Set a DNS name or IP without a port only
  when the certificate name differs. It controls verification, SNI, and HTTP/2
  authority; gateways must accept that authority without a port. Empty strings
  are invalid. IP addresses require matching IP SANs and send no SNI. Prefer
  a DNS name for IPv6 overrides because strict HTTP/2 servers reject an
  unbracketed IPv6 authority.
- TLS targets accept `host:port`, `dns:///host:port`, and
  `dns://authority/host:port`, including bracketed IPv6 hosts. Ports must be
  unsigned decimal values from 1 through 65535. The DNS resolver authority
  never becomes the certificate name. Other resolver schemes remain available
  with plaintext only.
- Constructors raise `ValueError` for invalid addresses, server names, or PEM
  bundles. Connections open on demand; certificate and handshake failures
  surface on the first Client call as `UNAVAILABLE` or during Worker startup.
  Servers offering only `http/1.1`, or no ALPN protocol, are rejected.
- This option does not configure the `ApiKeyCredential` token endpoint, which
  uses HTTPS except on loopback.

## Workers

Workers receive tasks through outbound pull streams. `Worker` and `AsyncWorker`
need only an outbound connection to the Dex Server at `server_address`. Configure
capacity and shutdown with `WorkerOptions.pull`; its default is
`PullWorkerOptions()`. Clients and Flows use pull routing automatically.

StartFlow and SubFlow always select pull Workers. Updating Flow configuration
preserves its existing Worker target; an empty `FlowConfig()` changes nothing.
Before upgrading an application with active push Flows, use an SDK version that
supports Worker targets to migrate those Flows to pull routing.

```python
import threading
from dex import Client, ClientOptions, PullWorkerOptions, Worker, WorkerOptions

worker = Worker(
    registry,
    cache,
    WorkerOptions(
        server_address="dex.example:8801",
        pull=PullWorkerOptions(max_concurrent_tasks=8),
    ),
)
worker_thread = threading.Thread(target=worker.start)
worker_thread.start()
worker.wait_pull_registered(timeout=30)
client = Client(
    registry,
    cache,
    ClientOptions(server_address="dex.example:8801"),
)
try:
    client.start_flow(flow, flow_id, input_value)
finally:
    client.close()
    worker.stop()
    worker_thread.join()
```

The Worker opens exactly `max_concurrent_tasks` long-lived streams. Each stream
is one execution slot serving the sticky queue and every registered Flow type.
Flow type names must be unique within a Dex server. Concurrency defaults to the
CPU count. Start fails if the configured concurrency exceeds the Server's
reported `matching.maxConcurrentTasksCap`; zero means no limit was reported.
The Worker checks the limit on its first registration only. It then half-closes
its streams, drops any task they receive, and never reports readiness;
registration waiters raise the limit error. A lower limit reported on a later
reconnect is logged as a warning, and the Worker keeps running.
`idle_streams_per_queue` is deprecated, remains accepted, and has no effect.
Each slot executes every task it receives and never rejects a task for capacity.
Step heartbeats and stream writes use the same stream. Busy streams send a Pong
every 20 seconds until the final frame.

The Worker rejects an unregistered Step or RPC by name before its handler runs.
Dex tries another Worker without consuming a Step attempt, including when the
Flow was sticky to the rejecting Worker. If no connected Worker supports the
Step type, the Step stays queued until a Worker that supports it connects, the
Flow times out, or the Flow is stopped. An RPC caller receives the named error
after the Server's dispatch wait.

The generated `app_worker_id` uses the first nonempty `DEX_WORKER_INSTANCE`,
`POD_NAME`, `HOSTNAME`, or system hostname, plus eight random base32 characters.
An override must uniquely identify one Worker process and change after restart.
It must contain 1-63 ASCII letters, digits, dots, underscores, or hyphens and
must not equal a registered Flow type.

Pull streams reconnect after failures. A broken stream cancels its handler;
its output is never sent on a replacement connection. Server-directed reconnect
lets an active task finish before sending its final output and half-closing.
If the Server has not confirmed that final output by EOF when the reconnect wait
of `stop_drain_timeout` ends, the stream gets up to 5 more seconds before it is
reset.
`start()` reports protocol and startup synchronization failures. Later transport
errors are logged and retried by default.

`stop()` starts no new tasks. Idle streams half-close immediately; busy streams
finish their handlers and send final output before half-closing. Task frames
arriving after stop begins are dropped without calling a handler. Streams keep
reading until server EOF or the remaining `stop_drain_timeout`, which defaults
to 30 seconds. On timeout, a busy stream is reset and its handler is canceled;
stop waits for the handler to return. Synchronous handlers should check
`Context.is_cancellation_requested()` during long operations. Any stream still
open at the deadline is reset, even one that is half-closed and still waiting
for server EOF, so stop returns once handlers return. Dex treats a task it sent
in that window as an attempt that may have run.

A stream whose final output was queued or written, but not yet confirmed by
server EOF or by the next assignment on that stream, is not reset at the drain
deadline. This holds even if its handler has finished or the stream has
half-closed, because a completed local write does not mean the Server read the
bytes. The stream gets up to 5 more seconds to send the output, half-close, and
receive server EOF, and is reset after that. `stop()` can therefore return up
to 5 seconds after the drain deadline.

A stream that fails locally, such as a read error, an undecodable or oversized
frame, a task frame with an invalid deadline or no request, a failed final
output write, or an unexpected server EOF, is always reset. It never
half-closes cleanly, because a clean half-close tells Dex the assigned task did
not run. A malformed task is never admitted and its handler never runs. Poll
streams accept only uncompressed responses.

After a slot sends a task's final output, Dex may assign its next task
immediately. The slot finishes local cleanup for the previous task first, then
starts the next one, unless stop has begun.

Call `worker.wait_pull_registered(timeout=30)` after starting a synchronous
Worker on another thread. For AsyncWorker, start it in another task and use
`await worker.wait_pull_registered(timeout=30)`. The timeout is in seconds;
`None` waits indefinitely. Each waits for one `STEP_AND_RPC` stream and, when
RPC reservation is configured, one `RPC_ONLY` stream to register. Timeout raises `TimeoutError`;
async cancellation cancels only that wait. Startup failures reach waiters.
A permanent rejection before readiness raises `PullRegistrationError`, whose
`queue` and `error` identify the rejected slot kind and original transport error.
Stopping before readiness raises `RuntimeError`.

Use `worker.pull_registration_state()` for subsequent readiness probes. Its
immutable `queues` expose `queue`, `has_registered_stream`, and
`last_permanent_error`. After initial registration, `is_ready` stays true across
transient failures, reconnects, and stop. It becomes false only when a required slot kind
has a permanent error and no registered stream. Re-registration clears the
error. The startup wait keeps succeeding after initial registration, including
after stop. Multiple callers may wait independently before or after start.

`PullWorkerOptions(stop_on_permanent_stream_rejection=True)` makes the first
permanent rejection leaving a slot kind without a registered stream drain within
`stop_drain_timeout`, then raise `PullRegistrationError` from `start()`. This
applies before and after readiness. Another registered stream for that slot kind
prevents shutdown. The default is False and retries indefinitely. Always call
`stop()` or `close()` afterward; both remain safe after `start()` returns.

### Reserved RPC capacity

Set `reserved_rpc_tasks` when synchronous RPC latency must stay responsive
behind long-running Steps. When unset, it reserves one slot if the resolved task
limit is at least two, otherwise zero. With the CPU-count default, Steps get one
fewer slot than before. Set it explicitly to zero to opt out. With one slot and
no explicit reserve, Worker start logs one warning: RPCs may wait behind
long-running Steps; use a limit of at least two or set the reserve explicitly.
Flows must stay responsive because RPCs share Worker task capacity with Steps.
The value must be nonnegative and less than `max_concurrent_tasks` after its
CPU-count default is resolved; invalid values fail construction. RPC and Step
handlers count together. Reserved slots register as `RPC_ONLY`; the remaining
slots register as `STEP_AND_RPC`. The Server assigns Steps only to the latter.
The Worker never declines a Step for capacity.

```python
pull = PullWorkerOptions(max_concurrent_tasks=8, reserved_rpc_tasks=2)
```

The `pull_reserved_rpc_declines` counter is deprecated and always zero. It stays
available for source compatibility. Registration snapshot `queues` and error
`queue` fields retain their names but now hold slot kind labels, `STEP_AND_RPC`
and `RPC_ONLY`, rather than individual queue names.

## Flow state I/O

Applications write Flow state through typed RPCs. Client and AsyncClient do not
expose direct Attribute writes, Channel publication, or pending-message
mutation. This keeps each external state transition behind a Flow-owned method.

Read committed Attribute values without an RPC through `get_attributes`. Pass
one or more singleton Attributes or AttributeMap instances selected with
`AttributeMap.instance(key)`:

```python
values = client.get_attributes(flow_id, ORDER_STATUS, NOTES.instance("n1"))
status = values.get(ORDER_STATUS, "new")
if NOTES.instance("n1") in values:
    note = values[NOTES.instance("n1")]
```

`AsyncClient.get_attributes` is the awaitable form. There is no single-Attribute
method; pass one read to read one value. The Client sends every key in one
request and no Worker runs, so all values come from the same committed state.
The call reads the current run, or the latest run when no run is open, and a
closed run stays readable until Dex purges it. Unlike a read inside an RPC
handler, it never sees writes that a running Step or RPC has not committed.

The result `AttributeValues` is keyed by the reads passed to the call, and type
checkers infer each value type from its key. An unset Attribute or map instance
is absent: `values[key]` raises `KeyError`, `values.get(key)` returns `None`,
`values.get(key, default)` returns `default`, and `key in values` is `False`. A
stored value that decodes to `None` is still present. Each read must equal a
definition declared by some Flow in the Client's Registry, with the same name,
value type, index, `sync_to_attribute_store`, and `hard_delete_on_null`;
otherwise the call raises `FlowDefinitionError` before contacting Dex. Two
reads of the same key, or no read at all, raise `ValueError`. Large values are
resolved through LoadBlobs and the BlobCache before decoding.

RPC handlers can stage `channel.delete(context, message_id)`. Declare the RPC as
`@rpc(is_transactional=True)` when a missing message must abort its other writes.
Attribute locks already select transactional execution, but Channel deletion
itself does not.

## RPC state loading

RPCs receive ordinary Attributes and all Channel size metadata by default.
AttributeMap entries and pending Channel messages are opt-in:

```python
@dex.rpc(
    load_attribute_map_instances=(items.load("tenant-a"),),
    load_channels=(queued,),
    load_channel_maps=(by_tenant,),
)
def snapshot(self, context: dex.Context) -> dex.RPCResult[Snapshot]:
    messages = queued.pending_messages(context)
    return dex.RPCResult(Snapshot(messages=messages))
```

Put an AttributeMap or ChannelMap directly in its plural load option when every
current instance is needed. Use the singular instance options for the less common
exact-instance case. A selected empty queue returns an empty tuple; reading an
unselected map entry raises **AttributeMapNotLoadedError**. An unselected pending-message
snapshot raises **ChannelMessagesNotLoadedError**.
Pending messages preserve FIFO order and include the server-assigned message ID.
The snapshot does not change after the handler stages a publish or deletion.

Loading controls which data reaches the Worker. **is_transactional** controls
atomic commit and Channel deletion validation. Attribute locks add isolation
only among cooperating Steps and RPCs using the same lock. Write-only publish
and delete operations do not require loading.

### Invocation-time map instances

The `@rpc` decorator remains the registration-time contract for timeout,
transactionality, locks, and fixed state loads. Pass frozen `RPCInvokeOptions`
through the keyword-only `options` argument when the caller chooses an exact map
instance at runtime:

```python
options = dex.RPCInvokeOptions(
    lock_attribute_map_instances=(profiles.lock(partition_name),),
    load_attribute_map_instances=(profiles.load(partition_name),),
)
result = client.invoke_rpc(
    directory.upsert_customer_profile,
    flow_id,
    profile,
    options=options,
)
```

`Client` and `AsyncClient` union, sort, and deduplicate these selections with the
registered ones. Locking and loading are independent; read-modify-write handlers
need both for the same instance.

See the runnable [sequential chunking](../examples/python/dex_examples/patterns/sequentially-chunked-attribute-map)
and [hash partitioning](../examples/python/dex_examples/patterns/hash-partitioned-attribute-map)
patterns for complete async call sites.

## Step and timeout-handler state loading

`StepOptions` provides the same five selections independently for `wait_for` and
`execute`: all AttributeMap instances, exact AttributeMap instances, Channels,
all ChannelMap instances, and exact ChannelMap instances. The methods receive
independent snapshots. Execute reads after the winning Wait consumes messages;
retries of one logical method call reuse its first snapshot.

`FlowTimeoutHandlerOptions` provides Execute-style timeouts, heartbeat detection,
retry, durability, Attribute locks, and the same state selections. Set it on
`StartFlowOptions` or `SubFlowOptions` only for a positive timeout using `HANDLER`.
Exhausted retries may proceed to a registered `Step[None]`; read the final
failure from `Context.recovery_error`.

```python
class TimeoutRecoveryStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        failure = context.recovery_error
        if failure is None:
            return force_fail("timeout recovery has no failure")
        return force_fail(failure.detail)
```

Python SDK for [Dex durable workflows](https://github.com/superdurable/dex)

## New user contracts

The rewrite targets Python 3.11+ and exposes strongly typed workflow contracts
from `dex`. This phase includes definitions, attributes, channels, streams, waits,
decisions, codecs, registry validation, synchronous client calls, and synchronous
worker handlers. Python owns its gRPC Client and Worker transport;
the shared Rust Core is used only for BlobCache.

```python
from datetime import timedelta
from typing import Generator

import dex

counter = dex.Attribute("counter", int)
counters_by_region = dex.AttributeMap("counters-by-region", int)
progress = dex.Stream("progress", str, 10 * 1024 * 1024)

class Run(dex.Step[str]):
    def wait_for(
        self, context: dex.Context, input: str
    ) -> dex.Wait:
        return dex.Wait.until(
            dex.Timer.by_duration(timedelta(seconds=1))
        )

    def execute(
        self, context: dex.Context, input: str
    ) -> Generator[dex.StepOutput, None, dex.StepDecision]:
        yield progress.write(context, "running")
        yield dex.heartbeat({"phase": "running"})
        return dex.graceful_complete(input)

class CounterFlow(dex.Flow[str]):
    run = Run()

    def get_flow_type(self) -> str:
        return "Counter"

    def get_steps(self) -> dex.StepList[str]:
        return dex.StepList.start_step(self.run)

    def get_persistence_schema(self) -> dex.PersistenceSchema:
        return dex.PersistenceSchema.of(counter, counters_by_region, progress)

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

`PersistenceSchema.of(...)` accepts attributes, channels, and streams together and
partitions them by definition type.

`Worker` and `AsyncWorker` synchronize all registered Indexed Attributes with
Dex Server before opening outbound task streams. Existing indexes return immediately;
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
config = dex.FlowConfig(attribute_store_names=["profiles", "audit"])
```

Stores are asynchronous latest-state projections. Every enabled Attribute write
is sent to every selected Store. Deletion writes SQL `NULL`, and projection
failures do not roll back Flow Attributes. `None` preserves current targets;
an explicit empty list disables future synchronization while retaining protocol
presence.

Declare short-lived secrets, such as API keys, with `hard_delete_on_null=True`:

```python
api_key = dex.Attribute("api-key", str, hard_delete_on_null=True)
provider_tokens = dex.AttributeMap("provider-token", str, hard_delete_on_null=True)
```

Dex stores these values only in its blob store, never in Flow history. Deleting
the Attribute or setting it to `None` permanently deletes the stored value, and a
deleted value reads as absent. Only string and object values, including
`bytes`, are supported; `int`, `bool`, and `float` types are rejected. The
option cannot be combined with an index or `sync_to_attribute_store`, and
`wait_for_attribute_match` rejects it. `get_attributes` reads it; Dex resolves
the stored value. Handlers and `get_attributes` receive the value inline, and
the SDK never stores it in the local blob cache.

Hard-delete writes require a Server with an enabled blob store. This SDK
requires protocol 4. Workers negotiate before startup and Clients negotiate
lazily before their first service request. Incompatible Servers fail with
`RuntimeError` before values are sent.

Each Flow keeps one stored copy per Attribute key, overwritten in place. A Step
or RPC attempt writes or deletes the stored value before its result commits, so
retries, other Steps, and RPCs can observe a write or deletion from an attempt
that later failed. Concurrent writers of one key may leave a value other than
the last committed write. A failed `start_flow` or SubFlow start for a running
Flow ID still overwrites, or for `None` deletes, the running Flow's value.
Executions that reuse a Flow ID share the value, and time travel does not
restore a deleted value. Values remain until deleted or until retention and blob
cleanup remove the Flow. Blob store backups or bucket versioning can retain
copies.

```
pip install dex-python-sdk==0.1.0
```

See [samples](../examples/python) for use case examples.

### JSON dataclasses

Automatic JSON codecs support parameterized generic dataclasses such as
`Result[Receipt]`, including nested generics, typed containers, `Optional[T]`, and
`T | None`. Decoding restores nested dataclass instances. Missing keys use the
field's `default` or `default_factory`; a missing required key raises `ValueError`
with the dataclass and wire field name. Unknown keys are ignored.

Field metadata controls the wire representation:

- `field(metadata={"dex_json_name": "callId"})` uses `callId` instead of the
  Python field name for encoding and decoding. Wire names must be non-empty and
  unique within a dataclass.
- `field(default=None, metadata={"dex_json_omit_default": True})` omits a field
  only when it equals its declared default. This also supports `default_factory`;
  the factory is called on each encode to obtain the comparison value, so use a
  deterministic factory without side effects. Without this opt-in, default
  values, empty containers, and `None` remain present in JSON.
- `field(metadata={"dex_json_format": "rfc3339"})` formats a datetime field like
  Go's `encoding/json`: UTC uses `Z` and trailing fractional zeroes are removed.
  This requires a datetime or optional datetime field with an aware value and a
  whole-minute timezone offset. Ordinary datetime fields keep their existing
  `isoformat()` encoding. All datetime fields accept Go RFC3339 strings on
  decode, including `Z`, numeric offsets, and fractional seconds. Python retains
  microsecond precision; additional fractional digits are truncated.

Metadata options can be combined on one field. Existing dataclasses retain their
field names and encoded values unless metadata opts into a change.

Unions select by JSON kind: null, boolean, number, string, array, or object.
A union declared directly, such as a Step input of `Receipt | None`, needs
alternatives with different kinds. `Receipt | None`, `int | str`, and
`int | bool` are supported. `int | float`, `str | datetime`, two dataclasses,
or two list types are ambiguous and raise `TypeError` when the codec is
resolved. Use an explicit `JsonCodec` with a discriminator for these shapes.
Union values always use JSON on the wire, including primitive alternatives.

Unions inside dataclass fields and containers keep the decoding of earlier
releases and never raise when the codec is resolved. Such a union decodes a
value into a dataclass, Enum, or datetime alternative, or a container of those,
when the value's JSON kind matches only that alternative. It returns any other
value unchanged, without a type check. Fields typed by a type variable and
`Mapping` or `Sequence` types follow the same rule: `Result[Receipt]` decodes a
`Receipt`, but `Result[int]` returns the JSON value of its type-variable field
unchanged. A `float` accepts a JSON integer and keeps it as an `int`.

See `help(CodecRegistry)` for a complete generic dataclass example and
[`tests/fixtures/json_codec`](tests/fixtures/json_codec) for a reproducible
Go-to-Python wire fixture.

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
  Worker accepts ordinary synchronous handlers and generator handlers. A generator
  yields `StepOutput` progress frames and returns its final `Wait` or `StepDecision`.
  With `AsyncWorker` and `Registry(..., allow_async_handlers=True)`, Step
  coroutines use `AsyncContext`; async RPCs keep `Context`.

Steps are registered by `get_step_type()`, which defaults to the class name.
Multiple instances of one class may return different Step types in the same Flow.
A duplicate Step type fails registration. `go_to`, `StepMovement.of`,
`StepOptions.on_execute_failure_proceed_to`,
`FlowTimeoutHandlerOptions.on_failure_proceed_to`, and decision or RPC
cancellation selections accept a registered instance, `StepRef("StepType")`, or
a class. A class must have exactly one registered instance; otherwise select an
instance or use `StepRef`. Instances match by identity and must belong to the
containing Flow, so a new instance of a registered class is rejected.

Declared execute-failure targets are validated when the Registry is constructed.
The Registry also rejects an execute-failure target whose input type differs from
the failing Step's input type. It skips this check when either type is `Any`,
`object`, or an unresolved TypeVar. Timeout-handler failure targets are validated
when Flow start options are mapped. Handler-created movements and cancellation
selections, including StepRef targets, are validated when the Worker applies the
returned decision; unknown Step types and ambiguous classes raise
`FlowDefinitionError` with the Flow and target details. StepRef contains only a
Step type and cannot execute or be registered in a StepList. Its input is checked
by the destination codec at runtime.

For a generic Step whose input annotation is a TypeVar, override
`get_input_type()` to return a concrete type supported by `CodecRegistry`. The
override takes precedence over both `execute` and `wait_for` input annotations;
Context and return annotations are still required. The default returns `None`,
which preserves annotation inference; return `type(None)` for a Step that accepts
no input. The Registry resolves the type through its codec registrations, so
custom codecs work too. See the runnable
[generic Step instances test](tests/integ/step_instances_flow.py), which registers
integer and string instances and routes to both by instance and StepRef.

`StepOptions.wait_for_method_timeout` and `execute_method_timeout` bound the
two handler calls. Timer and channel conditions determine how long a Step waits.

`wait_for_retry` and `execute_retry` limit one logical handler execution.
Attempts use one-based numbers. The retry total duration starts when Dex first
hands the Step to a Worker. After an attempt fails, Dex checks that budget to
decide whether to schedule another retry; it does not end a running attempt.

The default Step durability is synchronous. A Flow configuration can select
asynchronous durability, and a Step method override has highest precedence. The
default retry total duration is four hours. Attempts default to a two-hour
method timeout and one-minute heartbeat timeout; an explicit heartbeat timeout must
meet the server minimum, which defaults to ten seconds. Asynchronous durability
gives lower latency and faster single Flows. Sustained throughput is about the
same as synchronous durability.

### Flow run start time

`Context.flow_started_at` returns the current Flow run's server start time as a
timezone-aware UTC `datetime` with one-second precision. It stays the same across
Steps and retries in that run. It is available in `wait_for`, `execute`, Flow
timeout handlers, and RPC handlers for both Worker and AsyncWorker. Compare it
with `datetime.now(timezone.utc)` to measure the run's age. It matches
`Client.describe_flow(flow_id).started_at` to the second.

### Step attempt deadline

`Context.deadline` is the absolute deadline supplied by the Dex Server for the
current `wait_for`, `execute`, or Flow timeout handler attempt. It is a
timezone-aware UTC `datetime` with microsecond precision, not a duration.
Subtract `datetime.now(timezone.utc)` and call `total_seconds()` to budget
remaining work after checking for `None`. Each retry receives its own
effective deadline. RPC handlers and tasks without a supplied deadline return
`None`.

### Step progress and heartbeat recovery

A synchronous handler yields every heartbeat and Stream write. The generator return
value is the only final result:

```python
def execute(
    self, context: dex.Context, input: str
) -> Generator[dex.StepOutput, None, dex.StepDecision]:
    yield dex.heartbeat({"offset": 10})
    yield progress.write(context, "processed 10 items")
    return dex.graceful_complete(input)
```

An asynchronous handler keeps its normal coroutine return. Stream writes enqueue
without waiting for Stream Store acknowledgement; heartbeat waits only for the
Worker output queue:

```python
async def execute(
    self, context: dex.AsyncContext, input: str
) -> dex.StepDecision:
    writer = progress.buffered_text(context)
    writer.write("started")
    await context.heartbeat({"offset": 10})
    return dex.graceful_complete(input)
```

The async buffered writer has a synchronous `write` method, so `writer.write`
can be passed directly as an LLM delta callback. It flushes after one second, at
a soft 16 KiB UTF-8 threshold, or before the final result or error. It
concatenates chunks exactly and ignores empty chunks.

A synchronous generator uses the cooperative form because only yielded
`StepOutput` values can reach gRPC:

```python
writer = progress.buffered_text(context)
yield from writer.write(delta)
yield from writer.flush()
```

Its interval is checked by the next write, and the handler must explicitly
flush the tail. Retry does not restore either writer's unsent buffer or
deduplicate sent batches.

Call `heartbeat()` or `await context.heartbeat()` without a value to clear previous
details. Passing Python `None` persists a present null Value. On a later regular
attempt, use `context.has_last_heartbeat_value()` before decoding with
`context.get_last_heartbeat_value(ExpectedType)`. A Stream frame is also an implicit
heartbeat, but it preserves the latest explicit heartbeat state.

A Step may write the same Stream any number of times. Dex assigns
`#<stepExecutionID>` as each Step message's `StreamMessage.source`. Client writes
provide their own non-empty source; duplicate sources and `#` are allowed and every
write appends:

```python
client.write_stream(flow_id, progress, "frontend#preview", "rendering")
message = client.read_stream(flow_id, progress)
print(message.source)
```

`Client.read_stream` and `AsyncClient.read_stream` move forward one message at
a time and can long-poll. Their `list_stream_messages` methods return one
non-blocking newest-first retained page. Pass the registered `Stream`, a
positive page size, and an empty token for the first page. Pass
`StreamMessagesPage.next_page_token` unchanged to read older messages until it
is empty. The server caps page size at 1000 by default. A trimmed anchor returns
an empty page, and newer concurrent writes do not enter an existing older-page
chain.

### Canceling Step executions

A successful Step can cancel queued or active executions while continuing with
its normal decision:

```python
return (
    dex.go_to(RecordQuote, quote)
    .with_canceling_sibling_steps(QuoteCarrierA, QuoteCarrierB)
    .with_canceling_steps(GlobalQuoteTimeout)
)
```

`with_canceling_steps` selects every current execution of each registered Step
type. `with_canceling_sibling_steps` selects only executions with the same
`Context.from_step_execution_id` as the current execution. Decisions are
immutable; repeated calls form a union, and Flow-wide selection wins for the
same Step type. Unregistered selectors produce an invalid Step result.

Dex resolves one snapshot after the current execution succeeds. Completed,
already-canceled, and absent targets are no-ops. Next Steps created by the same
decision are outside the snapshot. Dex immediately applies the next or close
action; late decisions, writes, retries, and recovery Steps are discarded.

`RPCResult.with_canceling_steps` provides the Flow-wide selector for RPCs.
RPCs do not support sibling selection because they have no Step execution
lineage.

### Soft Flow timeout

Override `Flow.handle_timeout` to make a positive timeout use handler policy
by default. Both synchronous and async Workers support the hook:

```python
class Orders(dex.Flow[str]):
    async def handle_timeout(self, context: dex.Context) -> dex.StepDecision:
        await notify_expiration(context)
        return dex.force_complete("expired")

options = dex.StartFlowOptions(
    timeout=timedelta(minutes=30),
    timeout_policy=dex.FlowTimeoutPolicy.HANDLER,
)
```

Register async hooks with `allow_async_handlers=True` and run them with
`AsyncWorker`. `FAIL` produces `FlowErrorType.FLOW_TIMEOUT` and fails the
Flow; `CANCEL` cancels it. The deadline is preserved across Flow state
transfers. A zero or absent timeout
disables the feature.

`Registry` validates every Flow, Step, RPC signature, durable name, lock, and
codec before Client or Worker startup. `Client` methods use these typed objects
instead of raw Flow, Step, or RPC strings.

### Waiting and map inspection

`Wait.until`, `Wait.all_of`, and `Wait.any_of` use unnamed Conditions by
default. Do not add condition IDs merely because a Condition is nested in one
of these waits. Every Condition in `Wait.any_combination_of` must have a
non-empty user ID; the same Condition instance may appear in multiple
combinations.

Both `Client` and `AsyncClient` provide singleton and AttributeMap-instance
overloads of `wait_for_attribute_match`. Build matches with the six
`AttributeMatch` factories. The method targets the current run and returns the
decoded matched value. String and bool support equality operators; int and
float support every operator. JSON objects, bytes, null, non-finite floats, and
invalid ordering fail before transport. Every AttributeMap and ChannelMap instance must be non-empty
and must not contain `/`. `AttributeMap.get_map_size/get_all_instance_keys` include
buffered sets and deletes. The matching `ChannelMap` methods are RPC-only,
include buffered publishes, and omit empty instances. Keys are decoded and
sorted. Use `force_complete_if_channels_empty(...)` for conditional completion.

Request IDs are optional for both durable waits. When omitted, the server
derives a stable ID from the Step execution or Attribute condition. Reuse an
override only for the same wait. `request_timeout` is the total SDK call budget
across transparent transport reattachments. Zero waits indefinitely.
A caller timeout or cancellation does not finish an accepted durable wait. Set `internal_handler_timeout` only when abandoned waits can accumulate. An active call transparently registers its wait again. Prefer a value comfortably longer than
normal request timeouts and reconnect gaps. Zero disables timed rollover.
Request expiry raises `RequestTimeoutError`.

`Client.wait_for_flow` and `AsyncClient.wait_for_flow` return a
`FlowResult` after hydrating every output-bearing completion. Use
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
zero or multiple completions. Every terminal status returns a `FlowResult`; inspect
`status`, `error_type`, and `error_message` for unsuccessful completion.

SubFlows are normal, independently addressable Flows used as durable Conditions:

```python
def wait_for(self, context: Context, input: ChargeInput) -> Wait:
    return Wait.until(SubFlow.run(self.charge_flow, input))

def execute(self, context: Context, input: ChargeInput) -> StepDecision:
    del input
    receipt = SubFlow.get_condition_results(context).single_output(Receipt)
    return graceful_complete(receipt)
```

`SubFlow.get_flow_id(context, index=0)` remains available for a running `any_of`
loser. `SubFlowOptions` configures timing, timeout policy, retry, initial target
Attributes, Flow config, Condition ID, and reuse. Parent completion does not cancel an unfinished
SubFlow.

### Errors

Client calls raise concrete `DexServiceError` subclasses. Existing-Flow reads
(`describe_flow`, `get_attributes`, `wait_for_flow`, and `time_travel`) raise
`FlowNotFoundError` when the Flow does not exist. Mutations, RPCs, timer/Step
waits, config updates, and state-transfer triggers raise
`FlowNotActiveOrNotFoundError` when no running Flow can accept the operation.

RPCs also use this error when the target Flow cannot be found, including query-only
reads. A query-only RPC without locks, transactional execution, durable effects, or
Server-forced Update routing can read a retained terminal execution. At that confirmed
read boundary, this error means no readable target was found; return the application's
missing or unavailable result directly, without a lifecycle probe, a short timeout, a
retry, or historical Step-output decoding. Preserve other service and Worker failures.
For mutations and active-only RPCs, the error can mean either missing or closed and does
not prove that the requested action succeeded. RPC success does not prove that the Flow
is active.

```python
try:
    client.invoke_rpc(orders.update_order, order_id, update)
except dex.FlowNotActiveOrNotFoundError:
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

Update-class requests rejected during Flow close cleanup raise
`FlowClosingError` with sub-status `FLOW_CLOSING`. Re-read the retained terminal
Flow state before deciding whether another action is needed.

### Sync vs asyncio

- **Sync (default):** `Client` and `Worker` use blocking gRPC and a thread-pool
  Worker. A progress generator cooperatively hands each yielded frame to gRPC;
  `Stream.write` must therefore be yielded. Blocking Client calls inside
  `Step.execute` are safe while other pool threads remain available.
- **Asyncio:** `AsyncClient` and `AsyncWorker` use `grpc.aio`. Use
  `Registry(..., allow_async_handlers=True)` when Steps/RPCs are coroutines.
  Step coroutines annotate `AsyncContext`, call `Stream.write` without `await`,
  and await `context.heartbeat`. Inside async `execute`, inject `AsyncClient` —
  do not call sync `Client` on the Worker event loop. Async generators are rejected.

Integration scenarios live under
[`tests/integ`](tests/integ/README.md). They exercise the same workflows,
client operations, and assertions as the Java suite against an isolated
`dexcli dev` environment.

## Implementation status

The strongly typed contracts, registry, synchronous Client/Worker, optional
`AsyncClient`/`AsyncWorker` (`grpc.aio`), and Rust-backed BlobCache are
implemented. Python owns its gRPC transport; the native bridge is limited to
the shared BlobCache. Serialized object values use wire encoding `json` for JSON
and `raw` for raw bytes. Internal Blob references are opaque. Hydration sends the
owning Flow ID with each reference, and the cache is isolated by
`(flow_id, blob_ref)`. Design notes:
[`python-sdk-async-apis.md`](../docs/design/plan/python-sdk-async-apis.md) and
[`python-sdk-step-streaming.md`](../docs/design/plan/python-sdk-step-streaming.md).

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

The suite uses server-zd and pull Workers. `DEX_INTEG_ENGINE=zd` remains accepted.
Set `DEX_SERVER_ZD_BINARY` to reuse a built `dex-server-zd`.

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

## Server protocol compatibility

Workers call `GetServerInfo`, require protocol 4, synchronize Attribute indexes,
and then open execution slot streams. The Python SDK protocol interval is
`[4,4]`. Older Servers fail startup with this error, substituting the protocol
range and Server version actually reported:

```text
This SDK requires a Dex Server with protocol version 4 or later (server-zd newer than v0.1.16). The Server reports protocol versions <min>-<current> (server version <server_version>). Upgrade the Server before upgrading the SDK.
```

A Server whose minimum protocol is newer than 4 fails with an error that asks
you to upgrade the SDK. Missing information, invalid intervals, and RPC
failures stop startup before polling.

Clients perform no network I/O during construction. Before the first request of
any method, they negotiate protocol 4. Success and incompatibility are cached;
later requests against an incompatible Server raise the same clear
`RuntimeError`. If `GetServerInfo` itself fails, for example because the Server
is unreachable, the request raises the usual `DexServiceError`, and the next
call negotiates again. Negotiation counts against a request's timeout, including
the `request_timeout` of durable waits.

`RetryPolicy.total_duration` counts from the first hand-out to a Worker. After
an attempt fails, Dex checks it to decide whether to retry. It does not end a
running attempt. Recovery handlers can inspect `RecoveryErrorInfo.attempt_progress`:
`StepAttemptProgress.NOT_STARTED` guarantees no handler ran;
`NOT_COMPLETED` means some attempt may have run but none completed;
`UNSPECIFIED` covers older Servers or failures recorded before upgrade and must
be treated like `NOT_COMPLETED`.

The diagnostic SDK version comes from Python distribution metadata for
`dex-python-sdk` and falls back to `dev` when the distribution is unavailable.
It does not affect protocol compatibility.

## License

[Sustainable Use License 1.0](LICENSE), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).

## Search run inclusion

Searches exclude runs with the ContinuedAsNew status by default. server-zd never reports that status. Application queries still
need `FlowType` when using shared index slots. An explicit inclusion option
removes only the default exclusion; it does not override query filters. Keep
the query, inclusion option, and page size consistent across pages.

Pass keyword-only `include_continued_as_new=True` to synchronous or asynchronous `search_flows` for execution-chain inspection. The default is `False`.
