# Dex SDK for TypeScript

This package targets Node.js 22 and 24. It provides strongly typed workflow
contracts and a Promise-based gRPC Client. The Client and Worker runtime use
`@grpc/grpc-js`. The native BlobCache binding remains a separate runtime phase.

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
- `worker.ts`: Worker gRPC service and lifecycle
- `worker-dispatcher.ts`: typed callback dispatch and response mapping
- `invocation-context.ts`: invocation-scoped persistence and condition state
- `blob-cache.ts`: injectable cache contract and future N-API binding
- `gen/`: checked-in protobuf and grpc-js bindings

Run `npm test` for runtime contracts and `npm run typecheck` for strict static
contracts. Run `./run-integration-tests.sh` for all 58 IWF compatibility
scenarios against an isolated `dexcli dev` environment. Run
`npm run generate:proto` after changing `protos/dex.proto`; `protoc` and its
standard protobuf includes must be installed.

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
Update `package.json` and `package-lock.json` to the same version, merge the
change, then publish a GitHub Release tagged `sdk-typescript/vX.Y.Z`. The
release workflow verifies that the tag matches `package.json`, runs type checks
and tests, inspects the tarball, and publishes through npm Trusted Publishing.
Prerelease versions use the `next` npm dist-tag; stable versions use `latest`.

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
