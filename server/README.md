# Dex Server

Dex Server provides the FlowService API for durable application Flows. It
persists Flow state and dispatches Step and RPC tasks to application Workers.
Learn about the programming model in the [Dex documentation](https://docs.superdurable.io/).

## Local development

Use `dexcli` to start a complete local Dex environment:

```shell
brew install superdurable/tap/dexcli
dexcli dev
```

`dexcli dev` starts Dex Server, Dex Web, and the internal workflow backend. See
the [CLI README](../cli/README.md) for endpoints, persistence, and configuration
options.

## Deploy and contribute

For standalone server configuration and integration-test setup, see
[server contributor guidance](CONTRIBUTING.md). For production operations, see
the [server operations guide](../docs/content/production/server-operations.mdx).

Integration and replay test instructions are available in
[integ/README.md](integ/README.md) and [replayTests/README.md](replayTests/README.md).

## License

[Super Durable Source License 1.0](LICENSE.md), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
