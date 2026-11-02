# Dex - Durable Execution(D-EX) Engine

**Dex** provides a structural programming model as [durable primitives](https://docs.superdurable.io/primitives). 

You use Dex to write a Flow filled with native programming code: Steps, Attributes, RPCs, Conditions, Stream. Worker hosts the Flow code and pull tasks from Dex server.

Client calls Server to start and interact with Flow runs. Dex Server dispatches Step and RPC tasks to your Workers.

<img width="715" height="318" alt="dex-arch3" src="https://github.com/user-attachments/assets/57eb5344-8707-4fe1-9ed0-7896718a1a33" />



## Why Dex

### No Replay

Dex is created by **a core contributor to Uber Cadence&Temporal, and the original creator of iWF**—bringing deep, firsthand experience from generations of durable execution technology.

Unlike most durable execution frameworks, Dex takes a fundamentally different approach as **No Replay**. 

So why **"No Replay"** Matters?

Using our unique structural programming model:
1. Simpler & Cleaner APIs. Less primitives. Less code, less bug
2. Eliminate the "Non Deterministic Error", completely
3. Eliminate the "Continue-As-New" restriction, completely
4. No requires to rely on memory to cache workflow state as "sticky cache"
5. Data native -- explicit data storage primitives as "data attributes" (Note: no execution runs without data)
6. A large & growing [OSS library](https://github.com/superdurable/dex-connectors-library) to reuse, from prototype to production, as Step or Trigger.
7. Somehow visualizable (as long as code is written within certain format)

Learn more [about Dex & SuperDurable](https://superdurable.io/about/).

### Zero Dependencies

Dex requires no database, message queue or coordination service to depend on.

The only thing you need is S3 Object Storage(or compatible service).

Underlying, Dex built all the fundamental engine in Rust(with RocksDB) in a highly performant and optimized way.

Key idea:
* Every write is committed to S3 first(within a batch window to save S3 Puts cost) as "Command Logs", then apply to RocksDB as local cache
* RocksDB also generates snapshots(SST) that are flushed to S3 asynchronously
* When instance crashes, a standby(if any) or restarted instance can resume from the latest snapshots and re-apply the command logs to rebuild the durable state in seconds.
* Dex SDKs cache the attribute values in local disk to reduce network transport for large values.

The actual benefits:
1. Much higher performance & throughput, with low latency. See our performance [benchmark](./benchmark) that a very regular AWS EC2 instance can serve 10K to 50K reads/writes per second.
2. A strong support for the above "Data native". So you can just use Dex as a KV storage. Like you want to store the whole LLM context or thousands of messages in your agent run, you don't need another database.
3. Easy to operate. S3 is all you need.

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
* WeChat Community: please add **prclqz**   (龙全正) or **MUJIzz** (Zane) first.

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



