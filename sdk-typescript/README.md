# Dex SDK for TypeScript

This package targets Node.js 22 and 24. It provides strongly typed workflow
contracts and a Promise-based gRPC Client. The Client and Worker runtime use
`@grpc/grpc-js`. Blob caching uses the shared Rust DXBC implementation through
a Node-API addon.

Application values use `Codec<T>`. Flow, Step, RPC, Attribute, Channel, and Stream
definitions retain their input and output types. Client methods return Promise
because Node network I/O is asynchronous.

The Client uses `@grpc/grpc-js` directly. Rust is only the implementation
boundary for the shared BlobCache; TypeScript callbacks and network transport
stay in Node.

Omitted Step input codecs and RPC input/output codecs use JSON
(`JSON.stringify` / `JSON.parse`, no structural validation). Scalar wire kinds
still need `stringCodec`, `booleanCodec`, `int64Codec`, `doubleCodec`, or
`bytesCodec`. A `@rpc()` method with only `Context` and a `void` return stays a
procedure.

```typescript
class ApproveOrder implements Step<{ orderId: string }> {
  getStepType(): string {
    return "ApproveOrder";
  }

  waitFor(_context: Context, _order: { orderId: string }): Wait {
    return Wait.until(Timer.byDuration(1_000));
  }

  execute(_context: Context, order: { orderId: string }): StepDecision {
    return gracefulComplete(order);
  }
}

class Orders implements Flow<{ orderId: string }> {
  readonly approve = new ApproveOrder();

  getFlowType(): string {
    return "Orders";
  }

  getSteps() {
    return StepList.startStep(this.approve);
  }
}

const orders = new Orders();
const registry = new Registry([orders]);
```

Flows return all Steps once. Start with `StepList.startStep(step)` and append
heterogeneous Steps with `.otherSteps(...)`. Use
`StepList.withoutStartStep<void>(...)` for RPC-triggered Steps, or
`StepList.empty()` when the Flow has no Steps.
`Flow<StartInput>` only types the starting Step and `Client.startFlow()` input;
`StepList<StartInput>` enforces that relationship during type checking.
Non-starting Steps may use unrelated input types. `Flow` defaults to `void` for
Flows without a start input.

`Worker.start()` synchronizes every registered Indexed Attribute with Dex
Server before binding its listener. Existing indexes return immediately;
failure or the default 120-second deadline aborts startup. An indexed
`AttributeMap` must provide one fixed `indexKey`.

`StepOptions.waitForMethodTimeoutMs` and `executeMethodTimeoutMs` bound the two
handler calls. Timer and channel conditions determine how long a Step waits.

### Canceling Step executions

A successful Step can cancel queued or active executions while continuing with
its normal decision:

```typescript
return withCancelingSteps(
  withCancelingSiblingSteps(
    goTo(RecordQuote, quote),
    QuoteCarrierA,
    QuoteCarrierB,
  ),
  GlobalQuoteTimeout,
);
```

`withCancelingSteps` selects every current execution of each registered Step
type. `withCancelingSiblingSteps` selects only executions whose
`Context.fromStepExecutionId` matches the current execution. Both helpers return
a new decision; repeated calls form a union, and Flow-wide selection wins for
the same Step class. Unregistered selectors produce an invalid Step result.

Dex resolves one snapshot after the current execution succeeds. Completed,
already-canceled, and absent targets are no-ops. Next Steps created by that
decision are outside the snapshot. Dex immediately applies the next or close
action; late decisions, writes, retries, and recovery Steps are discarded.

An RPC result may set `cancelingSteps` for Flow-wide selection. RPCs do not
support sibling selection because they have no Step execution lineage.

### Soft Flow timeout

An optional `Flow.handleTimeout` makes a positive timeout use handler policy by
default. It may be synchronous or async and returns a normal `StepDecision`:

```typescript
async handleTimeout(context: Context): Promise<StepDecision> {
  await notifyExpiration(context);
  return forceComplete("expired");
}

await client.startFlow(orders, "order-42", input, {
  timeoutMs: 30 * 60_000,
  timeoutPolicy: FlowTimeoutPolicy.HANDLER,
});
```

`FAIL` produces `FlowErrorType.FLOW_TIMEOUT` and permits Flow retry; `CANCEL`
cancels without retry. Continue-as-new preserves the durable timer's deadline;
retry runs get a fresh budget. A zero or omitted timeout disables the
feature.

Opt an Attribute or AttributeMap into Attribute Store synchronization, then
select Server-configured Stores for the Flow:

```typescript
const email = new Attribute("customer-email", stringCodec)
  .syncToAttributeStore();
const config: FlowConfig = { attributeStoreNames: ["profiles", "audit"] };
```

Stores are asynchronous latest-state projections. Every enabled Attribute write
is sent to every selected Store. Deletion writes SQL `NULL`, and projection
failures do not roll back Flow Attributes. Omitting `attributeStoreNames`
preserves current targets; `attributeStoreNames: []` disables future
synchronization while retaining protocol presence.

### Waiting and map inspection

`Wait.allOf` and `Wait.anyOf` may contain unnamed Conditions and send empty
Condition IDs. Every Condition in `Wait.anyCombinationOf` needs a non-empty
user ID; the same Condition object may be reused across combinations.

`Client.waitForAttributeEqual` overloads target the current
run and accept only string, boolean, integer, or double wire values. JSON,
bytes, and null reject before transport. `AttributeMap.getMapSize` and
`getAllInstanceKeys` include buffered sets and deletes. The matching
`ChannelMap` methods are RPC-only, include buffered publishes, and omit empty
instances. Keys are decoded and sorted. Use
`forceCompleteIfChannelsEmpty(...)` for conditional completion.

### Async handlers

`Step.execute`, `Step.waitFor`, and RPC methods may be `async` and return a
`Promise` of their result. Because the Worker runs on Node's single event loop,
an async handler that `await`s `Client` calls (`startFlow`, `waitForFlow`,
`invokeRPC`, ...) yields the loop, so the **same** Worker keeps serving other
WorkerService calls — including the child Flow or RPC you just started. One
Worker is enough; no sidecar Worker is needed.

```ts
async execute(context: Context, childId: string): Promise<StepDecision> {
  await client.startFlow(childFlow, childId, input);
  const result = await client.waitForFlow(childId, 5_000);
  const output = result.singleOutput(stringCodec);
  return gracefulComplete(output);
}
```

Synchronous handlers stay valid — returning a plain `StepDecision` / `Wait` /
`RPCResult` needs no change. Two rules keep the loop healthy:

- Never block the event loop from a handler (`Atomics.wait`,
  `child_process.spawnSync`). That freezes the Worker and defeats the point.
- A long `await client.waitForFlow(...)` holds the current WorkerService call
  until it resolves or times out. Prefer a short timeout plus Step retry (or a
  timer-backed re-check) over one unbounded poll. `executeMethodTimeoutMs` /
`waitForMethodTimeoutMs` clock the full async handler duration.

### Step progress and Streams

Annotate a Promise-returning Step handler with `AsyncContext` when it needs to
record progress. The heartbeat value argument is required. Passing `undefined`
explicitly sends a heartbeat without a Value and clears previously persisted
heartbeat details. Passing `null` sends a present JSON-null Value.

```typescript
async execute(context: AsyncContext, input: ImportInput): Promise<StepDecision> {
  const restored = context.hasLastHeartbeatValue()
    ? context.getLastHeartbeatValue(importCheckpointCodec)
    : undefined;

  for await (const page of remainingPages(restored)) {
    importedRows.write(context, page.rows);
    await context.recordHeartbeat(page.checkpoint, importCheckpointCodec);
  }
  return gracefulComplete();
}
```

Omitting the heartbeat codec uses JSON. Read a restored value with the same
codec used to record it. `hasLastHeartbeatValue()` distinguishes an absent Value
from a present JSON-null Value; both decode to `undefined`.

`Stream.write(context, value)` is synchronous and fire-and-forget. It validates
and encodes locally, then writes a frame to the current WaitFor or Execute gRPC
response stream. A handler may write any number of messages to the same or
different Streams. Dex attempts each append in call order, but Stream Store
failures are not returned to the handler. RPC and Flow timeout Contexts reject
Step Stream writes.

External writes use `client.writeStream(flowId, stream, source, value)`. Source
must be non-empty, may contain `#`, and may repeat; every write appends a new
message. `Client.readStream` returns it as `StreamMessage.source`. Step writes
use `#<stepExecutionID>` as source metadata.

Step durability defaults to the Flow configuration and then sync. Regular
attempts default to two hours, heartbeat timeout defaults to one minute, and
retry total duration defaults to four hours. The SDK accepts non-negative
whole-second heartbeat timeout values, including two seconds, and leaves the
deployment-configured minimum to Dex Server. Async durability first uses a
seven-second local-activity window with at most three attempts; that phase
ignores method timeouts and heartbeats but still writes Stream messages.

Every TypeScript Flow and Step must return an explicit durable name from
`getFlowType()` or `getStepType()`. Class names are never used as fallbacks
because bundlers and minifiers may rename them.

`waitForFlow` hydrates every output-bearing completion before resolving. For a
multi-output Flow, select by Step identity and decode each value with its codec:

```typescript
const result = await client.waitForFlow(flowId);
const receipt = result.completions
  .find((completion) => completion.stepType === "ChargeCard")
  ?.decode(receiptCodec);
```

The completion array is read-only and keeps server collection order. Parallel
branch order is not deterministic. A no-output Flow returns an empty array;
`singleOutput` throws for zero or multiple completions. Every terminal status returns
a `FlowResult`; inspect `status`, `errorType`, and `errorMessage` for unsuccessful
completion.

SubFlows are normal, independently addressable Flows used as durable Conditions:

```typescript
public waitFor(_context: Context, input: ChargeInput): Wait {
  return Wait.until(SubFlow.run(this.chargeFlow, input));
}

public execute(context: Context, _input: ChargeInput): StepDecision {
  const receipt = SubFlow.getConditionResults(context).singleOutput(receiptCodec);
  return gracefulComplete(receipt);
}
```

`SubFlow.getFlowId(context, index)` remains available for a running `anyOf` loser.
`SubFlowOptions` configures timing, timeout policy, retry, initial target Attributes,
Flow config, Condition ID, and reuse. Parent completion does not cancel an unfinished SubFlow.

## Errors

Client calls reject with concrete `DexServiceError` subclasses. Existing-Flow
reads (`getAttribute`, `describeFlow`, `waitForFlow`, and `timeTravel`) use
`FlowNotFoundError`; operations that require a running Flow use
`FlowNotActiveError`. Start conflicts, worker failures, RPC lock contention,
and long-poll timeouts use `FlowAlreadyStartedError`,
`WorkerInvocationError`, `RpcLockConflictError`, and `LongPollTimeoutError`.

```typescript
try {
  await client.publish(flowId, orders.approved, orderId);
} catch (error) {
  if (error instanceof FlowNotActiveError) {
    // The Flow is missing or already closed.
  } else {
    throw error;
  }
}
```

Every service error retains `code`, `subStatus`, `detail`, `operation`,
`flowId`, and the original gRPC error as `cause`. `WorkerInvocationError` also
retains `workerCode`, `workerErrorType`, and `workerErrorDetail`. Registration,
serialization, and invalid handler returns use `FlowDefinitionError`,
`ValueMappingError`, and `InvalidStepResultError` instead of transport errors.

## Source layout

Public contracts are grouped by domain under `src/`. The root `src/index.ts`
is a barrel that re-exports the supported package API; applications should
continue importing only from `@superdurable/dex`.

- `codec.ts`: wire values and codecs
- `persistence.ts`: attributes, indexes, locks, and schemas
- `wait.ts`: channels, timers, conditions, and waits
- `step.ts`: Steps, movements, options, and decisions
- `rpc.ts`: typed RPC contracts and decorators
- `flow.ts`: Flows, registration, and validation
- `client.ts`: Promise-based FlowService Client
- `worker.ts`: Worker gRPC service and lifecycle
- `worker-dispatcher.ts`: typed callback dispatch and response mapping
- `invocation-context.ts`: invocation-scoped persistence and condition state
- `blob-cache.ts`: DXBC BlobCache contract and Node-API binding loader
- `gen/`: checked-in protobuf and grpc-js bindings

Run `npm run build:native` once to stage the DXBC Node addon for the current
platform, then `npm test` for runtime contracts, `npm run typecheck` for strict
static contracts, and `npm run docs:check` for public API documentation. The
documentation check follows the actual exports of `src/index.ts` and requires
JSDoc for every public class, interface, type, overload, member, object-style
enum value, type parameter, input, and output; generated sources are excluded.
The comments appear in TypeScript language-service and IDE hovers.

Run `./run-integration-tests.sh` for all 58 IWF
compatibility scenarios against an isolated `dexcli dev` environment. After
changing `protos/dex.proto`, run `make generated-code` from the repository root
and commit every server and SDK output.

## Integration coverage

Run the complete integration suite with TypeScript source coverage:

```shell
npm run coverage:integration
```

The terminal report lists coverage per SDK source file and every uncovered
line. Open `coverage/index.html` for annotated source, or inspect
`coverage/coverage-summary.json` programmatically. `coverage/lcov.info` is the
report uploaded by CI. Generated protobuf code under `src/gen/` is excluded.

CI uploads the LCOV report to Codecov with GitHub OIDC, so no upload secret is
stored in this repository. The report uses the `sdk-typescript-integration`
flag and contributes to the TypeScript SDK component defined in the root
`codecov.yml`. After the first successful `main` upload, Codecov displays
project and patch coverage in its dashboard, GitHub checks, and PR comments.
The Actions run also publishes the complete HTML report as
`sdk-typescript-integration-coverage`.

The complete legacy IWF integration inventory lives under
[`test/integ`](test/integ/README.md). Its Flow fixtures retain the Java
suite's workflow behavior and its 58 assertions run against a real Dex server.

## Releases

The npm package is published as [`@superdurable/dex`](https://www.npmjs.com/package/@superdurable/dex).
After the initial bootstrap, publish a GitHub Release tagged
`sdk-typescript/vX.Y.Z`. The tag is the release version source of truth; CI
temporarily writes it to `package.json` and `package-lock.json` without
committing either file. The release workflow then runs type checks and tests,
inspects the tarball, and publishes through npm Trusted Publishing. Prerelease
versions use the `next` npm dist-tag; stable versions use `latest`.

Trusted Publishing can only be configured after the package exists. Bootstrap
the first version from a maintainer workstation with 2FA:

```shell
cd sdk-typescript
npm ci
npm run typecheck
npm test
npm pack --dry-run
npm login
npm publish --access public
```

Then open the package settings on npmjs.com and add a GitHub Actions trusted
publisher with organization `superdurable`, repository `dex`, workflow
`sdk-typescript-publish.yml`, no environment, and `npm publish` permission.
Future releases use short-lived OIDC credentials and require no `NPM_TOKEN`.
After verifying the first OIDC release, configure npm publishing access to
require 2FA and disallow token-based publication.
