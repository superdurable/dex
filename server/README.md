# Dex project - main & server repo

[![Slack Status](https://img.shields.io/badge/slack-join_chat-white.svg?logo=slack&style=social)](http://dex-slack.work)
[![Go Reference](https://pkg.go.dev/badge/github.com/superdurable/dex.svg)](https://pkg.go.dev/github.com/superdurable/dex)
[![Go Report Card](https://goreportcard.com/badge/github.com/superdurable/dex)](https://goreportcard.com/report/github.com/superdurable/dex)
[![Coverage Status](https://codecov.io/github/superdurable/dex/coverage.svg?branch=main)](https://app.codecov.io/gh/superdurable/dex/branch/main)
[![Static Badge for Temporal Code Exchange](https://img.shields.io/badge/Temporal-Code_Exchange_Featured-blue?style=flat-square&logo=temporal&labelColor=141414&color=444CE7)](https://temporal.io/code-exchange/indeed-workflow-framework-dex)

[![Build status](https://github.com/superdurable/dex/actions/workflows/ci-cadence-integ-test.yml/badge.svg?branch=main)](https://github.com/superdurable/dex/actions/workflows/ci-cadence-integ-test.yml)
[![Build status](https://github.com/superdurable/dex/actions/workflows/ci-temporal-integ-test.yml/badge.svg?branch=main)](https://github.com/superdurable/dex/actions/workflows/ci-temporal-integ-test.yml)


# What is Dex
Indeed Workflow Framework(Dex) is a coding framework with service to streamlines workflows that involve waiting on external events, handling timeouts, 
and persisting state over long durations. With Dex, developers can build scalable, maintainable workflows that adapt to real-time events and integrate seamlessly with external systems. 

## What Makes Dex Unique 
* **Workflow-As-Code** uses native code to define everything: branching, looping, parallel threads, variables, schema etc.
* **Structured Programming** provides well-orginized structure to maintain workflow that is natural and easy to read.
* **Durable Timer** provides timer that is durable, resilient to system failure.
* **Automatic Retry** the background execution units(WorkflowState) are inherently resilient to failure, with built in distributed backoff retry using durable timer.
* **Simplified Architecture** Dex applications are all REST based micro-services which are easy to deploy, monitor, scale, maintain(version) and operate with industry standards.
* **Simplicity and explicitness of APIs** uses as few concepts as possible to model complex logic. It uses clear abstractions to defines workflows in terms of discrete states, with waitUntil conditions and execute actions, declarative schema for data and search attributes for persistence, and RPC for external interaction for both read and write.
* **Dynamic Interactions** allows external applications to interact with running workflows through RPC, signals, and internal channels.
* **Extensive tooling** provides tooling to look up running state definitions, skipping timers, enhanced resetting etc.

## Use case study/examples
* [SAGA pattern](https://medium.com/@qlong/saga-pattern-deep-dive-with-indeed-workflow-engine-b7e82c59e51f?sk=672abd70b0e092d4cda7788276c5a241)
  * [Java samples](https://github.com/superdurable/dex/tree/main/examples/java/src/main/java/io/dex/workflow/money/transfer), [Golang samples](https://github.com/superdurable/dex/tree/main/examples/go/workflows/moneytransfer), [Python samples](https://github.com/superdurable/dex/tree/main/examples/python/moneytransfer)
* [User sign-up/registry in Python/Java](https://github.com/superdurable/dex/wiki/Use-case-study-%E2%80%90%E2%80%90-user-signup-workflow)
* [Abstracted microservice orchestration in Java/Golang](https://github.com/superdurable/dex/wiki/Use-case-study-%E2%80%90%E2%80%90-Microservice-Orchestration)
* Employer & JobSeeker engagement in [Java](https://github.com/superdurable/dex/tree/main/examples/java/src/main/java/io/dex/workflow/engagement) or [Golang](https://github.com/superdurable/dex/blob/main/examples/go/workflows/engagement)
* Subscription Workflow in [Java](https://github.com/superdurable/dex/tree/main/examples/java/src/main/java/io/dex/workflow/subscription) or [Golang](https://github.com/superdurable/dex/blob/main/examples/go/workflows/subscription)
* [Design Patterns](https://medium.com/@qlong/dex-design-patterns-936a48336766)

## Basic concepts
* [Basic concepts overview](https://github.com/superdurable/dex/wiki/Basic-concepts-overview)
* [WorkflowState](https://github.com/superdurable/dex/wiki/WorkflowState)
* [RPC](https://github.com/superdurable/dex/wiki/RPC)
* [Persistence](https://github.com/superdurable/dex/wiki/Persistence)

See more in [Dex wiki](https://github.com/superdurable/dex/wiki).

# How to use

As a coding framework, Dex provides three SDKs to use with:

* [Dex Java SDK](https://github.com/superdurable/dex-java-sdk) and [samples](https://github.com/superdurable/dex/tree/main/examples/java)
* [Dex Golang SDK](https://github.com/superdurable/dex/tree/main/sdk-go) and [samples](https://github.com/superdurable/dex/tree/main/examples/go)
* [Dex Python SDK](https://github.com/superdurable/dex-python-sdk) and [samples](https://github.com/superdurable/dex/tree/main/examples/python)

The Dex SDKs required a server to run against. See below options to run the server locally. See [Dex wiki](https://github.com/superdurable/dex/wiki) for production 

## Using all-in-one docker image

This is the simplest option to run the server locally for development.

Run the docker command to start the container for:
* DEX service: http://localhost:8801/
* Temporal WebUI: http://localhost:8233/
* Temporal service: localhost:7233
```shell
docker pull superdurable/dex-server-lite:latest && docker run -p 8801:8801 -p 7233:7233 -p 8233:8233 -e AUTO_FIX_WORKER_URL=host.docker.internal --add-host host.docker.internal:host-gateway -it superdurable/dex-server-lite:latest
```

## Using docker image & docker-compose

This option runs Temporal in separate container with slightly more power (more search attributes allowed).

Checkout this repo, and run:

```shell
docker pull superdurable/dex-server:latest && docker-compose -f ./docker-compose/docker-compose.yml up
```

This by default will run Temporal server with it, again:
* DEX service: http://localhost:8801/
* Temporal WebUI: http://localhost:8233/
* Temporal service: localhost:7233

## Production
Check the [wiki](https://github.com/superdurable/dex/wiki/Dex-Server-Operations#how-to-deploy).

# Support

Join our Slack channel! [![Slack Status](https://img.shields.io/badge/slack-join_chat-white.svg?logo=slack&style=social)](http://dex-slack.work)

You can also post in our [Discussion](https://github.com/superdurable/dex/discussions), or raise an issue.

# Contributing

Check out our [CONTRIBUTING](https://github.com/superdurable/dex/blob/main/CONTRIBUTING.md) page.


# Posts & Articles & Reference
* [Why I created Indeed Workflow Framework](https://medium.com/@qlong/a-letter-to-cadence-temporal-and-workflow-tech-community-b32e9fa97a0c)
* [Dex on Temporal CodeExchange](https://temporal.io/code-exchange/indeed-workflow-framework-dex)
* [14 “Modern” Backend Software Design Patterns with Indeed Workflow Framework(Dex) on Temporal](https://medium.com/@qlong/dex-design-patterns-936a48336766)
* [Dex Overview for Temporal Users](https://medium.com/@qlong/dex-overview-for-temporal-users-part1-programming-model-difference-9f58e4793cfa)
* [Build Reliable AI Agents with Indeed Workflow Framework on Temporal](https://medium.com/@qlong/build-reliable-ai-agents-with-dex-on-temporal-7f1a101e000b)
* Cadence community spotlights
  * [#1](https://cadenceworkflow.io/blog/2023/01/31/community-spotlight-january-2023/)
  * [#2](https://cadenceworkflow.io/blog/2023/11/30/community-spotlight-update-november-2023/)
  * [#3](https://cadenceworkflow.io/blog/2023/08/31/community-spotlight-august-2023/)
* Dex is an abstracted Temporal [framework](https://github.com/temporalio/awesome-temporal). Same for [Cadence](https://github.com/uber/cadence#cadence).
* [How ContinueAsNew is built in Dex](https://medium.com/@qlong/guide-to-continueasnew-in-cadence-temporal-workflow-using-dex-as-an-example-part-1-c24ae5266f07)
## License

[Super Durable Source License 1.0](LICENSE.md), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
