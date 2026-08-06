# IWF integration compile port

This directory mirrors the complete Java `iwfcompat` inventory: 29 Flow
fixtures and 16 integration scenario files. Every Flow has its own module;
`iwf_flows.ts` only creates the shared instances used by scenarios.

The scenario functions are compile-only examples. They are not registered with
Node's test runner and never contact a Dex server.

Static verification only:

```shell
npm run typecheck
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
