<p align="center">
  <a href="https://github.com/stablyai/orca"><img src="https://img.shields.io/github/stars/superdurable/dex?style=flat&amp;label=%E2%98%85&amp;color=08C" alt="GitHub stars" /></a>
  <a href="https://discord.gg/V9cMHXts98"><img src="https://img.shields.io/badge/Discord-5865F2?logo=discord&logoColor=white" alt="Join the SuperDurable Discord" /></a>
</p>

# Durable Execution(D-EX). No Replay, Zero Dependencies.

**Dex** provides a structural programming model as [primitives](https://docs.superdurable.io/primitives) as Durable Execution Engine.  

You use Dex to write a Flow filled with native programming code: Steps, Attributes, RPCs, Conditions, Stream. Worker hosts the Flow code and pull tasks from Dex server.

Client calls Server to start and interact with Flow runs. Dex Server dispatches Step and RPC tasks to your Workers.

<img width="1020" height="473" alt="dex-arch4" src="https://github.com/user-attachments/assets/5d22c06f-851d-41f8-aae8-62dbb5588cd7" />



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
6. A large & growing [connector library](https://github.com/superdurable/dex-connectors-library) to integrate with internet products(Stripe,Gmail etc), from prototype to production, as Step or Trigger.
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
1. High throughput with low latency. In our [benchmark](./benchmark), one server-zd node on a 16-vCPU AWS c7gd.4xlarge peaked at about 11K Steps/s, 12K Attribute-updating RPCs/s, and 49K Attribute-reading RPCs/s. A 2-vCPU c7gd.large peaked at about 1.4K Steps/s and 7.4K Attribute-reading RPCs/s.
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

## 🎉Community to connect with us and other users!💬

* [![Discord](https://img.shields.io/badge/Discord-Join%20Community-5865F2?logo=discord&logoColor=white)](https://discord.gg/V9cMHXts98)
* **WeChat Group**: Scan to add add **prclqz** (龙全正) or **MUJIzz** (Zane) first:
<img width="300" height="300" alt="Dex WeChat Group" src="https://github.com/user-attachments/assets/3069ad7c-a905-4ab6-a174-2005ad8d45d5" />


## Repository layout

| Path | Contents |
| --- | --- |
| [server-zd/](server-zd/) | Rust Dex Server |
| [server-zd/integ/](server-zd/integ/) | Go integration suite for the server-zd binary |
| [web/](web/) | Standalone Dex Web console and `dex-web` binary |
| [cli/](cli/) | `dexcli` local development environment |
| [protos/](protos/) | Protobuf IDL and codec server |
| [s3local/](s3local/) | Local S3-compatible object store for testing purpose|
| [blob-cache-go/](blob-cache-go/) | Shared Go disk blob cache |
| [benchmark/](benchmark/) | Performance benchmarks |
| [packages/](packages/) | Shared Web packages |
| [agents/](agents/) | Adapters for Agent SDKs |
| [sdk-go/](sdk-go/), [sdk-java/](sdk-java/), [sdk-python/](sdk-python/), [sdk-typescript/](sdk-typescript/), [sdk-rust/](sdk-rust/) | Application SDKs |
| [examples/](examples/), [docs/](docs/), [skills/](skills/) | Runnable examples, product docs, and AI skills |



