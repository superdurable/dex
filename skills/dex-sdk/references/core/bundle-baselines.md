# Dex Skills bundle baselines

Use these immutable release tags when the loaded Skill needs to inspect the
reference sources packaged with this Dex Skills release. Project dependencies
and lockfiles remain authoritative for application code.

```text
DEX_BASELINE=sdk-go/v1.30.0
DEX_SERVER_BASELINE=server-zd/v0.1.13
DEX_CLI_BASELINE=cli-v1.30.0
DEX_WEB_BASELINE=web/v1.30.0
DEX_JAVA_BASELINE=sdk-java/v1.30.0
DEX_PYTHON_BASELINE=sdk-python/v1.30.0
DEX_TYPESCRIPT_BASELINE=sdk-typescript/v1.30.0
DEX_RUST_BASELINE=sdk-rust/v1.26.0
```

These values mirror the release metadata at the Dex Skills repository root.
The package validator rejects any drift between the two copies.

Go, Java, Python, and TypeScript are at their published v1.30.0 tags. Rust is
at v1.26.0. The Server baseline is server-zd/v0.1.13, the CLI is
cli-v1.30.0, and Dex Web is web/v1.30.0. The
Temporal-based Go Server was removed in Dex 1.26; its final release is
server/v1.25.0, and server-zd is the only Dex Server. cli-v1.26.0 was tagged
without binaries, so never pin it. The CLI release package bundles
`dex-server-zd` server-zd/v0.1.13, which is the Server baseline. Dex Web runs as
the standalone `dex-web` binary or `superdurable/dex-web` image, released as
web/vX.Y.Z.

v1.20.0 added the typed FLOW_CLOSING error in every SDK. Go
v1.21.0 added **Client.DescribeFlow**, and v1.22.0 added
**ClientOptions.FlowServiceTLS** and **WorkerOptions.FlowServiceTLS**. v1.23.0
keeps one extra idle pull stream after a reserved-RPC decline and sends a
busy-stream Pong every 20 seconds in all five SDKs. v1.25.0 removes listening
Workers from every SDK, so every Worker is a pull Worker. v1.26.0 removes the
Go Server internals and the push fields (`WorkerTarget.Address`,
`WorkerTarget.IsHeadlessAddress`) from the protocol bindings. Python and
TypeScript v1.28.0 add **Context.deadline**, instance-addressed Steps with
**StepRef**, and (Python only) **Context.flow_started_at** and the extended
JSON codec. Go v1.25.0 added **Client.GetAttributes**, and Python and
TypeScript v1.29.0 add **Client.get_attributes**, **AsyncClient.get_attributes**,
and **Client.getAttributes**; Java and Rust do not have the Client Attribute
read yet. cli-v1.28.0 and web/v1.28.0 add FDG 2.0 for Python and TypeScript
sources and Connector package manifests. v1.30.0 adds two protocol
sub-statuses for the PostgreSQL search backend to the generated bindings of
Go, Java, Python, and TypeScript; no public SDK API changes. The server-zd
guidance covers v0.1.3 through v0.1.13.
Each SDK and its language examples use that language's exact tag. **DEX_BASELINE**
is the Go SDK pin, not a shared version for every language.
At their baseline tags, the Rust example manifest selects SDK v1.25.0, the Java example manifest selects SDK v1.26.0, and the Go, Python, and TypeScript example manifests select SDK v1.29.0.
They are runnable source examples, not the API-version authority.
API key Credentials need SDK v1.16.0. Their guidance therefore comes from each
SDK's source and README at its language tag, not from the examples.
The Go **RecoveryErrorInfo** time-limit predicates need Go SDK v1.17.0, so
their guidance comes from the Go SDK source and README at its tag.
The Client Attribute read excerpts come from the SDK READMEs at their language
tags. The Go, Python, and TypeScript Attribute primitive examples also use it.
Hard-delete Attributes need SDK v1.11.0, so their
excerpts come from the SDK READMEs and SDK integration tests at the corresponding language tag. Go SDK
v1.5.0 changed default Go Flow and Step type names; see
[Go versioning](../go/versioning.md#default-flow-and-step-type-names).
For a new project, select the released SDK version deliberately. For an
existing project, inspect its installed SDK and lockfile before applying
precise syntax or changing dependencies.
