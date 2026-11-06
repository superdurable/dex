# TypeScript handbook

Read this page first for TypeScript application work. It gives the
TypeScript rules only. Concepts and guarantees live in core:
[Flow](../core/flow.md), [Step](../core/step.md),
[Attribute](../core/attribute.md), [RPC](../core/rpc.md),
[Channel](../core/channel.md), [Stream](../core/stream.md),
[Timer](../core/timer.md), [SubFlow](../core/subflow.md),
[Connector](../core/connector.md), [Client](../core/client.md),
[Application](../core/application.md), [error handling](../core/error-handling.md),
[testing](../core/testing.md), and [patterns](../core/patterns.md).

| Task | Page |
| --- | --- |
| Write Flows, Steps, state, RPCs, Streams, SubFlows, Client calls | [primitives](primitives.md) |
| Apply a design pattern | [patterns](patterns.md) |
| Client boundaries, retries, recovery | [error handling](error-handling.md) |
| Verify against a real Dex Server | [testing](testing.md) |

## Version rule

Find the SDK version the project resolves before you rely on an API detail:

```bash
npm ls @superdurable/dex
```

Open the [TypeScript SDK reference](https://docs.superdurable.io/reference/typescript/)
and pick that minor in the left-sidebar version selector. API signatures,
defaults, units, and errors live in the TSDoc comments of the installed
package (`node_modules/@superdurable/dex/dist/src/*.d.ts`). The installed
declarations win over anything remembered. Do not invent an API that the
declarations do not contain.

## Project shape

- Import everything from `@superdurable/dex`. The package is ESM only. Use
  `"type": "module"` and `module`/`moduleResolution` `NodeNext`.
- Use a supported Node.js LTS line. The SDK reference lists them.
- Keep Flow and Step classes in application modules. Keep HTTP handlers in
  separate controller modules.
- Construct every Flow instance once, in one registry module. Inject business
  services through Flow and Step constructors.
- Create one `Registry`, one `BlobCache`, one `Worker`, and one `Client` per
  process. The Worker and the Client share the same Registry and BlobCache.
- When a Step needs the Client (for example to start another top-level Flow),
  inject a holder that is set after bootstrap. Do not import the Client from
  a module that the Flow module also imports.
- `@rpc` is a TC39 standard (stage 3) decorator. Keep
  `experimentalDecorators` off. When the runtime cannot transform decorators,
  such as Node.js type stripping, use `declareRPCMethod` instead
  ([RPC](primitives.md#rpc)).

Runnable layout: [examples/typescript](https://github.com/superdurable/dex/tree/main/examples/typescript)
([registry](https://github.com/superdurable/dex/blob/main/examples/typescript/src/registry.ts),
[bootstrap](https://github.com/superdurable/dex/blob/main/examples/typescript/src/main.ts),
[Worker startup](https://github.com/superdurable/dex/blob/main/examples/typescript/src/config/worker.ts)).

## Wiring skeleton

`GreetingFlow` is any Flow class ([primitives](primitives.md#flow-and-step)).

```typescript
import { Client, Registry, Worker, openBlobCache } from "@superdurable/dex";

export async function startDexApplication(serverAddress: string, blobCacheDirectory: string) {
  const greetingFlow = new GreetingFlow();
  const registry = new Registry([greetingFlow]);
  const blobCache = openBlobCache({ directory: blobCacheDirectory, maxBytes: 1 << 30 });
  const worker = new Worker(registry, blobCache, { serverAddress });
  try {
    await worker.start();
    await worker.waitPullRegistered({ timeoutMs: 30_000 });
  } catch (failure) {
    await worker.close();
    blobCache.close();
    throw failure;
  }
  const client = new Client(registry, blobCache, { serverAddress });

  async function stop(): Promise<void> {
    await client.close();
    await worker.close();
    blobCache.close();
  }

  return { greetingFlow, client, worker, stop };
}
```

The sizes and timeouts above are placeholders. Take real values from the
application's configuration.

## Names and types

- `Flow.getFlowType()` and `Step.getStepType()` are required. Return a
  constant string. Name each Flow after its domain, such as `ApprovalFlow`,
  never just `Flow`. Flow types are unique in a Registry. Step types are
  unique in a Flow.
- These strings, Attribute, Channel, Stream, and RPC names, and codec wire
  shapes are durable identity. Open Flows resume on new code with them.
  Renaming a class is safe only when the returned type strings stay the same.
- TypeScript types are erased at run time. A `Codec<T>` is the runtime
  contract. Generic annotations do not validate anything.
- Scalar wire values need an explicit codec: `stringCodec`, `booleanCodec`,
  `int64Codec` (`bigint`), `doubleCodec` (`number`), `bytesCodec`,
  `voidCodec`. Omitted codecs mean JSON. A Step whose input is a `number` or
  `string` must set `inputCodec`. An RPC with a scalar input or output must
  set `inputCodec` or `outputCodec`.
- Use `jsonCodec<T>({ typeName, decode })` for objects. Validate in `decode`
  when the data is untrusted or evolves. Cross-language Flows depend on these
  wire shapes.
- Millisecond options are plain numbers named `...Ms`. Keep units in names.

## Handlers

- `execute`, `waitFor`, RPC handlers, and `handleTimeout` may be synchronous
  or return a Promise. Await every Promise you start.
- A handler that records heartbeats takes `AsyncContext` and is `async`.
  RPC handlers get `Context` and cannot record heartbeats.
- `Wait` is a description that `waitFor` returns. It is not a Promise. Never
  `await` it and never sleep in `waitFor`.
- Async work yields the event loop, so one Worker can serve the Flow or RPC
  that its own handler calls. Synchronous CPU work blocks every handler of the
  Worker. Split long CPU work or move it out of process.
- `context.cancellationSignal` is cooperative. Pass it to abort-aware APIs.
  Cancellation never undoes an external effect.
- Staged Attribute writes, Channel publishes, and deletes commit only with
  the handler's successful result. Module globals, files, and remote calls are
  not durable. A thrown error discards the staged writes.

## Worker lifecycle

Every Worker is a pull Worker. It needs only an outbound connection to the Dex
Server. No Worker address or routing target exists.

- `await worker.start()`. It negotiates the Server protocol, synchronizes
  Attribute indexes, and resolves after the first slot registers. A Server
  that is too old, unreachable, or rejects the configured capacity fails
  `start()`.
- Then `await worker.waitPullRegistered({ timeoutMs })` with a deadline before
  you report the process ready or stop the old Worker in a rolling deploy.
- Probe later readiness with `worker.pullRegistrationState().isReady`.
  Reconnects do not flap it.
- Use `worker.run()` instead of `start()` when a supervisor must observe a
  fatal stream rejection. Call only one of the two on a Worker.
- `PullWorkerOptions.maxConcurrentTasks` is the slot count.
  `reservedRpcTasks` keeps some slots for RPCs only. Steps are never declined
  for capacity. A reserved slot cannot bypass an Attribute lock held by a Step.
- On shutdown call `client.close()`, then `worker.close()`, then
  `blobCache.close()`. `worker.close()` stops taking tasks and lets running
  handlers finish within the drain timeout. Handlers still running at the
  deadline are canceled, and Dex treats those attempts as possibly run.
- Use a fresh `appWorkerID` after every restart if you set one.

Readiness, capacity, and shutdown settings:
[TypeScript Workers reference](https://docs.superdurable.io/reference/typescript/workers)
and [Server Workers reference](https://docs.superdurable.io/reference/server/workers).
Runnable: [pull-workers.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/production/pull-workers.ts).

## Authentication and TLS

- Set `credential` separately in `ClientOptions` and `WorkerOptions`.
  `FileCredential` reads a rotating token file. `ApiKeyCredential` exchanges
  an API key. A custom `Credential` returns the current token, or a Promise of
  it, for each call and each stream opening.
- Set `flowServiceTls` separately in `ClientOptions` and `WorkerOptions`.
  Omitting it keeps plaintext gRPC. Any value, even `{}`, uses TLS and never
  falls back to plaintext.
- Keep tokens out of Flow input, Attributes, and logs.

Details: [Server authentication reference](https://docs.superdurable.io/reference/server/authentication).

## Connectors

- Use an official Connector only when the published catalog lists
  `typescript` for the connector and for every operation and Trigger you need.
- Install the connector package `@superdurable/connector-<slug>` at the version
  the catalog gives. It depends on `@superdurable/connectors-sdk`.
- At startup call `await loadFromEnvironment()` from
  `@superdurable/connectors-sdk/projectconfig` once. Open each connection with
  the connector package's `newProjectConnection(project, connectionName)`.
  Pass the connection to the operation's Step factory, such as
  `newSearchArticlesStep(...)`, and register the Flow as usual.
- Do not write your own configuration loader. Do not copy credentials into
  Flow input or Attributes.
- Hold a short-lived secret that the Flow itself must keep in a hard-delete
  Attribute ([primitives](primitives.md#attribute-and-attributemap)).

## Dex Web and the FDG analyzer

- `dexcli visualize` reads TypeScript Flow source. Only Go Flows can publish
  the full Dex Web management metadata (Summary, Display, Actions, and
  indexed Attribute presentation). Do not invent TypeScript syntax for it.
- Put exactly one `// dex:group group-id:<id> group-label:"<Label>"` line and
  one `// dex:explanation text:"<one sentence>"` line directly above each
  local Step class, with no blank line between them and the class.
- The analyzer has TypeScript limits, such as Step classes that extend another
  class, dynamic access to Flow or Step fields, and decisions hidden in helper
  functions. Keep Step objects in the Flow file and return decisions directly.
  Check the [Flow visualization reference](https://docs.superdurable.io/reference/web/flow-visualization)
  for the current list.

## Gotchas

- Return the same Step instances from `getSteps()` on every call. Create them
  once as Flow fields.
- Pass the exact registered Flow instance to Client methods. A structurally
  identical new instance is not registered.
- `syncToAttributeStore()` and `hardDeleteOnNull()` return a new definition.
  Register and use the returned object everywhere.
- Inside a handler, an absent Attribute reads as the codec zero value for
  scalar numeric and Boolean codecs (`0`, `0n`, `false`) and as `undefined`
  otherwise. `client.getAttributes` returns `undefined` for absent values.
- Map instance keys are non-empty and contain no `/`.
- `void somePromise` hides rejections. Await, or attach a rejection handler and
  state who owns the failure.
- An unhandled rejection from `context.recordHeartbeat` can terminate Node.js.
  Always await it.
- Catch exported SDK error classes with `instanceof`, never by message text.
- A Stream is best effort. Keep authoritative state in Attributes or output.

## Completion checklist

- [ ] Every Step reachable by `goTo`, `StepMovement`, failure routes, or
      cancellation is in `getSteps()`, and every Attribute, Channel, and
      Stream is in `getPersistenceSchema()`.
- [ ] Each Flow and Step returns a stable, domain-named type string.
- [ ] Scalar inputs and outputs have explicit codecs. Objects use a validated
      `jsonCodec` where data is untrusted.
- [ ] Every Promise is awaited. Heartbeats use `AsyncContext`.
- [ ] Worker startup waits for registration with a deadline and closes on
      failure. Shutdown closes Client, Worker, then BlobCache.
- [ ] Client calls handle typed errors ([error handling](error-handling.md)).
- [ ] Tests run against a real Dex Server with unique Flow IDs
      ([testing](testing.md)).
- [ ] No application code depends on Dex Server internals.
