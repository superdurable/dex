# Dex SDK for TypeScript

This package targets Node.js 22 and 24. It provides strongly typed workflow
contracts and a Promise-based gRPC Client. The Worker runtime and native
BlobCache binding are the remaining runtime phases.

Application values use `Codec<T>`. Flow, Step, RPC, Attribute, and Channel
definitions retain their input and output types. Client methods return Promise
because Node network I/O is asynchronous.

The Client uses `@grpc/grpc-js` directly. Rust is only the implementation
boundary for the shared BlobCache; TypeScript callbacks and network transport
stay in Node.

Step input codecs and RPC input/output codecs remain explicit because
TypeScript erases generic types at runtime. They are serialization metadata,
not builder arguments.

```typescript
class ApproveOrder implements Step<string> {
  readonly inputCodec = stringCodec;

  getStepType(): string {
    return "ApproveOrder";
  }

  waitFor(_context: Context, _orderId: string): Wait {
    return Wait.allOf(Timer.byDuration(1_000));
  }

  execute(_context: Context, orderId: string): StepDecision {
    return gracefulComplete(orderId);
  }
}

class Orders implements Flow<string> {
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

`StepOptions.waitForMethodTimeoutMs` and `executeMethodTimeoutMs` bound the two
handler calls. Timer and channel conditions determine how long a Step waits.

Every TypeScript Flow and Step must return an explicit durable name from
`getFlowType()` or `getStepType()`. Class names are never used as fallbacks
because bundlers and minifiers may rename them.

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
- `worker.ts`: Worker runtime boundary
- `blob-cache.ts`: injectable cache contract and future N-API binding
- `gen/`: checked-in protobuf and grpc-js bindings

Run `npm test` for runtime contracts and `npm run typecheck` for strict static
contracts. Run `npm run generate:proto` after changing `protos/dex.proto`;
`protoc` and its standard protobuf includes must be installed.

The complete legacy IWF integration inventory has a compile-only port under
[`test/iwfcompat`](test/iwfcompat/README.md). Its 28 Flow fixtures and 16
scenario files show the TypeScript programming model without starting a
server.
