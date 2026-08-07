# IWF integration port

This directory mirrors the complete Java integration inventory: 28 Flow
fixtures and 16 integration scenario files. Every Flow has its own module;
`iwf_flows.ts` only creates the shared instances used by scenarios.

The `*.integration.test.ts` files port all 58 Java assertions and run through
the TypeScript Client and Worker against `dexcli dev`. The older scenario
functions remain compile-contract coverage for representative call sites.

Static verification:

```shell
npm run typecheck
```

Full integration verification:

```shell
./run-integration-tests.sh
```

## Programming experience

- Flow and Step are interfaces, while explicit codecs provide the runtime type
  information erased by TypeScript.
- `Wait.allOf(Timer.byDuration(...))` and domain factories keep nested calls
  readable.
- Decorated RPC method references preserve their input and output types through
  `Client.invokeRPC`.
- Attribute-map locks retain the instance through
  `items.lock("order-1")`.
- Client methods return Promise because blocking the Node event loop is unsafe.
