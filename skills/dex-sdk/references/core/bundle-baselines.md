# Dex Skills bundle baselines

Use these immutable release tags when the loaded Skill needs to inspect the
reference sources packaged with this Dex Skills release. Project dependencies
and lockfiles remain authoritative for application code.

```text
DEX_BASELINE=sdk-go/v1.25.0
DEX_SERVER_BASELINE=server/v1.25.0
DEX_CLI_BASELINE=cli-v1.25.0
DEX_JAVA_BASELINE=sdk-java/v1.25.0
DEX_PYTHON_BASELINE=sdk-python/v1.25.0
DEX_TYPESCRIPT_BASELINE=sdk-typescript/v1.25.0
DEX_RUST_BASELINE=sdk-rust/v1.25.0
```

These values mirror the release metadata at the Dex Skills repository root.
The package validator rejects any drift between the two copies.

All five SDKs are at their published v1.25.0 tags. The Server and CLI are at
server/v1.25.0 and cli-v1.25.0. v1.20.0 added the typed FLOW_CLOSING error in every SDK. Go
v1.21.0 added **Client.DescribeFlow**, and v1.22.0 added
**ClientOptions.FlowServiceTLS** and **WorkerOptions.FlowServiceTLS**. v1.23.0
keeps one extra idle pull stream after a reserved-RPC decline and sends a
busy-stream Pong every 20 seconds in all five SDKs. Server v1.24.0 limits Go
Server SearchFlows to Dex Flows and carries pull dispatch failure text into
SubFlow results. v1.25.0 removes listening Workers from every SDK, so every
Worker is a pull Worker. The server-zd guidance covers v0.1.3 through v0.1.8.
Each SDK and its language examples use that language's exact tag. **DEX_BASELINE**
is the Go SDK pin, not a shared version for every language.
The example dependency manifests in all five languages select SDK v1.15.0.
They are runnable source examples, not the API-version authority.
API key Credentials need SDK v1.16.0. Their guidance therefore comes from each
SDK's source and README at its language tag, not from the examples.
The Go **RecoveryErrorInfo** time-limit predicates need Go SDK v1.17.0, so
their guidance comes from the Go SDK source and README at its tag.
Hard-delete Attributes need SDK v1.11.0 and Server v1.11.0, so their
excerpts come from the SDK READMEs and SDK integration tests at the corresponding language tag. Go SDK
v1.5.0 changed default Go Flow and Step type names; see
[Go versioning](../go/versioning.md#default-flow-and-step-type-names).
For a new project, select the released SDK version deliberately. For an
existing project, inspect its installed SDK and lockfile before applying
precise syntax or changing dependencies.
