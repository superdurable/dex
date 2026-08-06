# IWF integration compile port

This directory mirrors the complete Java `iwfcompat` inventory: 29 Flow
fixtures and 16 integration scenario files. Every Flow has its own module;
`iwf_flows.py` only creates the shared instances used by scenarios.

The scenario functions are intentionally not pytest tests. They describe the
typed Client call sites without contacting a Dex server.

Static verification only:

```shell
uv run --frozen mypy tests/iwfcompat
uv run --frozen pyright tests/iwfcompat
```

## Programming experience

- Python annotations derive Step and RPC codecs; Attribute and Channel values
  declare ordinary Python types.
- `Wait.all_of(Timer.by_duration(...))` and handle methods keep nested calls
  readable.
- RPC methods remain normal bound methods and are passed directly to
  `Client.invoke_rpc`.
- Attribute-map locks retain the instance through
  `items.lock("order-1")`.
- Synchronous Client shapes match Java and Go while preserving Python naming.
