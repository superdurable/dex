# Dex Skills bundle baselines

Use these immutable release tags when the loaded Skill needs to inspect the
reference sources packaged with this Dex Skills release. Project dependencies
and lockfiles remain authoritative for application code.

```text
DEX_BASELINE=sdk-go/v1.26.0
DEX_SERVER_BASELINE=server-zd/v0.1.11
DEX_CLI_BASELINE=cli-v1.26.1
DEX_WEB_BASELINE=web/v1.26.0
DEX_JAVA_BASELINE=sdk-java/v1.26.0
DEX_PYTHON_BASELINE=sdk-python/v1.26.0
DEX_TYPESCRIPT_BASELINE=sdk-typescript/v1.26.0
DEX_RUST_BASELINE=sdk-rust/v1.26.0
```

These values mirror the release metadata at the Dex Skills repository root.
The package validator rejects any drift between the two copies.

All five SDKs are at their published v1.26.0 tags. The Server baseline is
server-zd/v0.1.11, the CLI is cli-v1.26.1, and Dex Web is web/v1.26.0. The
Temporal-based Go Server was removed in Dex 1.26; its final release is
server/v1.25.0, and server-zd is the only Dex Server. cli-v1.26.0 was tagged
without binaries, so never pin it. The CLI release package bundles
`dex-server-zd` server-zd/v0.1.11, which is the Server baseline. Dex Web runs as
the standalone `dex-web` binary or `superdurable/dex-web` image, released as
web/vX.Y.Z.

v1.20.0 added the typed FLOW_CLOSING error in every SDK. Go
v1.21.0 added **Client.DescribeFlow**, and v1.22.0 added
**ClientOptions.FlowServiceTLS** and **WorkerOptions.FlowServiceTLS**. v1.23.0
keeps one extra idle pull stream after a reserved-RPC decline and sends a
busy-stream Pong every 20 seconds in all five SDKs. v1.25.0 removes listening
Workers from every SDK, so every Worker is a pull Worker. v1.26.0 removes the
Go Server internals and the push fields (`WorkerTarget.Address`,
`WorkerTarget.IsHeadlessAddress`) from the protocol bindings. The server-zd
guidance covers v0.1.3 through v0.1.11.
Each SDK and its language examples use that language's exact tag. **DEX_BASELINE**
is the Go SDK pin, not a shared version for every language.
The example dependency manifests in all five languages select SDK v1.25.0.
They are runnable source examples, not the API-version authority.
API key Credentials need SDK v1.16.0. Their guidance therefore comes from each
SDK's source and README at its language tag, not from the examples.
The Go **RecoveryErrorInfo** time-limit predicates need Go SDK v1.17.0, so
their guidance comes from the Go SDK source and README at its tag.
Hard-delete Attributes need SDK v1.11.0, so their
excerpts come from the SDK READMEs and SDK integration tests at the corresponding language tag. Go SDK
v1.5.0 changed default Go Flow and Step type names; see
[Go versioning](../go/versioning.md#default-flow-and-step-type-names).
For a new project, select the released SDK version deliberately. For an
existing project, inspect its installed SDK and lockfile before applying
precise syntax or changing dependencies.
