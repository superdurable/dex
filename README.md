<p align="center">
  <a href="https://docs.superdurable.io"><img src="docs/static/img/brand/dex-mark.svg" alt="Dex logo" width="72" /></a>
</p>

<h1 align="center">Dex</h1>

<h3 align="center">Durable Execution (D-Ex). No Replay, Zero Dependencies.</h3>

<p align="center">
  <a href="https://github.com/superdurable/dex/stargazers"><img src="https://img.shields.io/github/stars/superdurable/dex?style=flat&amp;label=%E2%98%85&amp;color=08C" alt="GitHub stars" /></a>
  <a href="https://docs.superdurable.io"><img src="https://img.shields.io/badge/docs-docs.superdurable.io-008650" alt="Dex documentation" /></a>
  <a href="https://discord.gg/V9cMHXts98"><img src="https://img.shields.io/badge/Discord-5865F2?logo=discord&logoColor=white" alt="Join the Super Durable Discord" /></a>
  <a href="LICENSING.md"><img src="https://img.shields.io/badge/license-Sustainable%20Use%201.0-blue" alt="License: Sustainable Use License 1.0" /></a>
</p>

**Dex** is a lightweight durable execution platform for AI agents and workflows. Write normal code, and Dex keeps it running through crashes, deploys, and long waits: every finished step is saved, so an agent or a workflow picks up exactly where it left off.

```bash
brew install superdurable/tap/dexcli   # macOS or Linux
dexcli dev                             # starts a local Dex server and web UI
```

Then run your first Flow with the [Quick Start](https://docs.superdurable.io/quick-start), or let a coding agent build it with the official [Dex Skills](https://docs.superdurable.io/build-with-ai/dex-developer-skill).

> [!TIP]
> Building an AI agent? Keep your framework, such as the Claude Agent SDK or the OpenAI Agents SDK, and start with the **[Agent Quick Start](https://docs.superdurable.io/agent-frameworks/quick-start)**.

## Why use Dex?

Dex keeps *any* long-running agent or workflow running until it finishes:

- **[Durable agents](https://docs.superdurable.io/agent-frameworks)**: Build agents that survive crashes, deploys, and outages, retry failed calls, and resume from their last model or tool call.
- **[No replay](#no-replay)**: Write ordinary code. Dex resumes from the last saved step instead of re-running your code from the top.
- **[Zero dependencies](#zero-dependencies)**: Run one binary on object storage such as Amazon S3, with no database or message queue to operate.
- **[Human-in-the-loop](https://docs.superdurable.io/agent-frameworks/approvals)**: Pause for a person's approval or reply, for minutes or days. The wait survives restarts and deploys.
- **[Data native](https://docs.superdurable.io/concepts/primitives/attribute)**: Store a whole LLM context or thousands of messages in the Flow itself, as Attributes, with no extra database.
- **[Connectors](https://github.com/superdurable/dex-connectors-library)**: Call Stripe, Gmail, and other products through a large and growing library of ready-made Steps and Triggers, from prototype to production.
- **[Visibility and time travel](https://docs.superdurable.io/components/operate-and-debug)**: See every step's input and output in the Dex Web UI, and rewind a failed run to an earlier step after a fix.

## How it fits together

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/images/dex-system-dark.png" />
    <img width="703" alt="Your application holds a Client and a Worker, both built with the Dex SDK. The Client calls Dex Server with startFlow and invokeRPC. Dex Server invokes Steps and RPCs on the Worker and persists Flow instances and their execution." src=".github/images/dex-system-light.png" />
  </picture>
</p>

Your [Flow](https://docs.superdurable.io/concepts) code runs in a **Worker** inside your own application. **Dex Server** saves each step's result and dispatches the next step.

## No Replay

Most durable execution engines recover a process by *replay*: they run your code again from the top and feed it logged results, so the code must behave the same on every run. Dex takes a fundamentally different approach. Its structured programming model makes each step explicit, so Dex saves each step's result and continues from the next step. Nothing is replayed, which gives you:

- **Simpler, cleaner APIs**: fewer primitives, less code, fewer bugs.
- **No non-determinism errors**: a step can read the clock, use random values, and call any library.
- **No manual history rollover**: a Flow can run for months without being split into new runs.
- **No sticky cache**: Workers keep no workflow state in memory.
- **Visualizable**: Dex can draw a Flow as a graph from its source code, as long as the code follows a [supported format](https://docs.superdurable.io/reference/web/flow-visualization).

Dex is built by the original creator of [iWF](https://github.com/indeedeng/iwf), drawing on deep, firsthand experience with generations of durable execution technology ([about us](https://superdurable.io/about/)).

## Zero Dependencies

Dex needs no database, message queue, or coordination service. All it needs is object storage: Amazon S3 or a compatible service, Azure Blob Storage, Google Cloud Storage, or Aliyun OSS. The engine is written in Rust on RocksDB and built for performance:

- Every write is committed to object storage first, as a **command log** batched to save PUT costs, and then applied to RocksDB as a local cache.
- Snapshots are uploaded in the background. After a crash, a standby or a restarted node loads the latest snapshot, re-applies the log, and rebuilds its state in seconds.
- The SDKs cache large values on local disk, so they are not sent over the network again.

What you get:

- **High throughput with low latency**: in our [benchmark](benchmark/), one 16-vCPU node (AWS c7gd.4xlarge) peaked at about 8.4K Steps/s, 13.8K Attribute-updating RPCs/s, and 67K Attribute-reading RPCs/s, and a 2-vCPU node at about 1.8K Steps/s and 15K Attribute-reading RPCs/s. At half of peak load, an Attribute read took about 0.5 ms.
- **A built-in key-value store**: state lives in Dex, so an agent run needs no other database.
- **Easy to operate**: one binary and one bucket. To run nothing at all, use [Dex Cloud](https://docs.superdurable.io/components/dex-cloud).

## SDKs

```bash
pip install dex-python-sdk                          # Python
go get github.com/superdurable/dex/sdk-go@latest    # Go
npm install @superdurable/dex                       # TypeScript
cargo add dex-sdk                                   # Rust
```

For Java, add `io.superdurable:dex-sdk` from [Maven Central](https://central.sonatype.com/artifact/io.superdurable/dex-sdk).

## Documentation

[Quick Start](https://docs.superdurable.io/quick-start) · [Durable Agents](https://docs.superdurable.io/agent-frameworks) · [Run Dex](https://docs.superdurable.io/components) · [Concepts](https://docs.superdurable.io/concepts) · [Design Patterns](https://docs.superdurable.io/design-patterns) · [Evaluate Dex](https://docs.superdurable.io/evaluate) · [Developer Reference](https://docs.superdurable.io/reference) · [简体中文](https://docs.superdurable.io/zh-Hans/)

## Community

- [Discord](https://discord.gg/V9cMHXts98), to talk with us and other users.
- **WeChat group**: add **prclqz** (龙全正) or **MUJIzz** (Zane), or scan the code below, and we will invite you.

<img width="180" height="180" alt="QR code to join the Dex WeChat group" src="https://github.com/user-attachments/assets/3069ad7c-a905-4ab6-a174-2005ad8d45d5" />

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) to build and test each module. Contributors sign the [CLA](CLA.md).

<details>
<summary>Repository layout</summary>

| Path | Contents |
| --- | --- |
| [server-zd/](server-zd/) | Rust Dex Server |
| [server-zd/integ/](server-zd/integ/) | Go integration suite for the server-zd binary |
| [web/](web/) | Standalone Dex Web console and `dex-web` binary |
| [cli/](cli/) | `dexcli` local development environment |
| [protos/](protos/) | Protobuf IDL and codec server |
| [s3local/](s3local/) | Local S3-compatible object store for testing |
| [blob-cache-go/](blob-cache-go/) | Shared Go disk blob cache |
| [benchmark/](benchmark/) | Performance benchmarks |
| [packages/](packages/) | Shared Web packages |
| [agents/](agents/) | Adapters for agent frameworks |
| [sdk-go/](sdk-go/), [sdk-java/](sdk-java/), [sdk-python/](sdk-python/), [sdk-typescript/](sdk-typescript/), [sdk-rust/](sdk-rust/) | Application SDKs |
| [examples/](examples/), [docs/](docs/), [skills/](skills/) | Runnable examples, product docs, and AI skills |

</details>

## License

Dex is source-available under the Sustainable Use License 1.0. See the [licensing guide](LICENSING.md) and the [legacy notices](LEGACY_NOTICES.md).
