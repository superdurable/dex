# Dex - Durable Execution(D-EX) Engine

**Dex** provides a structural programming model as [durable primitives](https://docs.superdurable.io/primitives). 

You use Dex to write a Flow filled with native programming code: Steps, Attributes, RPCs, Conditions, Stream. Worker hosts the Flow code and pull tasks from Dex server.

Client calls Server to start and interact with Flow runs. Dex Server dispatches Step and RPC tasks to your Workers.

<img width="715" height="318" alt="dex-arch3" src="https://github.com/user-attachments/assets/57eb5344-8707-4fe1-9ed0-7896718a1a33" />



## Reasons to try Dex

### Why Dex #1: No Replay

Unlike most durable execution frameworks, **Dex takes a fundamentally different approach with its non-replay-based programming model**. It eliminates entire classes of footguns—such as replay-induced non-determinism errors and the need for Continue-As-New—by design, rather than forcing developers to work around them.

Dex is created by **a core contributor to Uber Cadence&Temporal, and the original creator of iWF**—bringing deep, firsthand experience from multiple generations of durable execution technology.

Dex is an evolution of a battle-tested architecture, shaped by years of building and operating durable execution systems at scale.

Learn more [about Dex & SuperDurable](https://superdurable.io/about/).

### Why Dex#2: Zero Dependencies

Dex requires no database, message queue or coordination service to depend on.

The only thing you need is S3 Object Storage(or compatible service).

Underlying, Dex built all the fundamental engine in Rust(with RocksDB) in a highly performant and optimized way.

Key idea:
* Every write is committed to S3 first(within a batch window to save S3 Puts cost) as "Command Logs", then apply to RocksDB as local cache
* RocksDB also generates snapshots(SST) that are flushed to S3 asynchronously
* When instance crashes, a standby(if any) or restarted instance can resume from the latest snapshots and re-apply the command logs to rebuild the durable state in seconds.
* Dex SDKs cache the attribute values in local disk to reduce network transport for large values.

See our performance [benchmark](./benchmark) that a very regular AWS EC2 instance can serve 10K to 50K reads/writes per second. 

## How to start
See [Quick start](https://docs.superdurable.io/quick-start) on docs.superdurable.io.

AI coding assistants can use the official [Dex Skills](https://docs.superdurable.io/build-with-ai/dex-developer-skill) to build, test, and operate Dex applications through Dex's public programming model. Install either the recommended Dex Plugin or the complete standalone Skills bundle from [superdurable/dex-skills](https://github.com/superdurable/dex-skills), but not both.

The [skills/](skills/) module contains the Dex Skills source.

You can see [examples/](./examples) of the repo.

### Durable Agents 
A common usage of dex is to make your agents "durable" -- survive from any disruptions like instance killed, service downtime with durable timer & retry. At the same time, Dex provide a webUI with history to show even step's input/output and operation like time-travel.

See [how to use Dex with the popular Agent SDKs](https://docs.superdurable.io/agent-frameworks/)

### SDKs
Use official SDKs and agent skills to make any of your processes reliable:

* Python
```bash
pip install dex-python-sdk
```

* Go
```bash
go get github.com/superdurable/dex/sdk-go@latest
```

* Java
```gradle
implementation "io.superdurable:dex-sdk:1.31.0"
```

* TypeScript
```bash
npm install @superdurable/dex
```

* Rust
```bash
cargo add dex-sdk
```

## 🎉Communityto connect with use and other users!💬

* [Slack](https://dexai.pro)
* [Discord](https://discord.gg/TetzsMpYb) 
* WeChat ID: **prclqz**   (龙全正)

## Repository layout

| Path | Contents |
| --- | --- |
| [server-zd/](server-zd/) | Rust Dex Server |
| [server-zd/integ/](server-zd/integ/) | Go integration suite for the server-zd binary |
| [web/](web/) | Standalone Dex Web console and `dex-web` binary |
| [cli/](cli/) | `dexcli` local development environment |
| [protos/](protos/) | Protobuf IDL and codec server |
| [s3mock/](s3mock/) | Local S3-compatible object store for testing purpose|
| [blob-cache-go/](blob-cache-go/) | Shared Go disk blob cache |
| [benchmark/](benchmark/) | Performance benchmarks |
| [packages/](packages/) | Shared Web packages |
| [agents/](agents/) | Adapters for Agent SDKs |
| [sdk-go/](sdk-go/), [sdk-java/](sdk-java/), [sdk-python/](sdk-python/), [sdk-typescript/](sdk-typescript/), [sdk-rust/](sdk-rust/) | Application SDKs |
| [examples/](examples/), [docs/](docs/), [skills/](skills/) | Runnable examples, product docs, and AI skills |



