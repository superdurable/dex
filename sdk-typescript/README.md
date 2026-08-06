# Dex SDK for TypeScript

This package targets Node.js 22 and 24 and contains the strongly typed public
contracts for the Dex SDK rewrite. Definitions, attributes, channels, waits,
decisions, codecs, and registry validation work now. Async client and worker
transport reject with `PhaseNotImplementedError` until the shared Rust Core is
connected.

Application values use `Codec<T>`. Flow, Step, RPC, Attribute, and Channel
definitions retain their input and output types. Client methods return Promise
because Node network I/O is asynchronous.

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
`StepList.withoutStartStep<void>(...)` for RPC-only Flows.
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
- `client.ts`, `worker.ts`, `blob-cache.ts`: runtime boundaries

Run `npm test` for runtime contracts and `npm run typecheck` for strict static
contracts.

The complete legacy IWF integration inventory has a compile-only port under
[`test/iwfcompat`](test/iwfcompat/README.md). Its 29 Flow fixtures and 16
scenario files show the TypeScript programming model without starting a
server.
