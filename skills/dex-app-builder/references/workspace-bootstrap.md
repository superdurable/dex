# Stage 0: workspace, stack, and local tooling

Inspect the repository before you select a language, create manifests, or
install dependencies. A repository is effectively empty when it has no
application source or build manifests. A README, license, editor
configuration, or other placeholder does not count.

The installed Dex Skills release that the coding-agent host loads is the only
skill authority. Never assume a fixed skill filesystem path. Never add a skill
submodule or a floating skill branch to the application repository.

## Existing application

Preserve the application's intentional structure, Go module, Dex SDK
requirement, `dexcli` expectation, lockfiles, generators, and commands. Reuse
its declared build, generation, and run commands instead of inventing parallel
ones. Change a dependency only when the user asks or a recorded compatibility
rule requires it. Explain the verification consequence first.

If the existing backend is not Go, FDG 2.0, Dex Web management metadata,
and the application Connector integration that App Builder relies on are
unavailable. Stop before implementation. Ask whether to add a Go backend or to
continue as an explicitly requested standalone `dex-sdk` project without those
capabilities.

## Effectively empty repository

Create a Go application that depends only on source-available Dex:

1. Install `dexcli` with the supported package for the platform. On macOS
   that is `brew install superdurable/tap/dexcli`. Run `dexcli version`.
2. Create the Go module. Add the Dex Go SDK at an exact released version with
   `go get github.com/superdurable/dex/sdk-go@<release>`. Choose the newest
   stable SDK release that pairs with the installed `dexcli` and the target
   Server. The [Developer reference](https://docs.superdurable.io/reference/)
   has the compatibility matrix. Never copy versions from a neighboring
   project, and never resolve a branch, pseudo-version, or commit.
3. Load the [Dex SDK skill](../../dex-sdk/SKILL.md) and follow its Core and Go
   routing for the Registry, Worker, Client, and first vertical slice. Copy
   API shapes from the runnable
   [Go examples](https://github.com/superdurable/dex/tree/main/examples/go).
4. Keep each Flow in its own source file named for its domain. Add one
   Registry that composes every Flow and one Worker entry point. Add an
   application HTTP server only for a confirmed custom UI or integration
   ingress. Add a frontend directory only for a confirmed custom UI.
5. Record the project's own commands for FDG rendering, code generation,
   checks, builds, and local runs in its README or build file. Later work
   reuses them.

In the first implementation update, name the selected Dex Go SDK and `dexcli`
releases. If a release cannot be resolved or verified, report that blocker.
Never fall back to an unreleased dependency.

TypeScript is limited to an optional frontend and its generated client. Do not
start a TypeScript, Node, or other non-Go Dex backend for an App Builder
product. Route an explicitly requested non-Go Dex application through
`dex-sdk`, and say that the Go-only Dex Web metadata is then unavailable.
A Python or TypeScript application may use an official Connector where the
catalog lists its language.

## Pair the SDK and the CLI

Project scripts and FDG rendering call the `dexcli` on `PATH`. Run
`dexcli version` first. The FDG analyzer must name Flow and Step types the same
way the Worker's Go SDK registers them. Pair `dexcli` with the project's Go SDK
release so the rendered graph matches the Worker. When the graph and Worker
names differ, fix the pairing. Do not add `GetFlowType` or `GetStepType`
overrides as a workaround.

When upgrading the global CLI would break other projects, install a
project-local `dexcli` and put its directory first on `PATH` for this project.

Before a Worker connects to a non-local Dex Server, run
`dexcli version check --server ADDRESS` against that Server. It reports the
highest common protocol and fails when the SDK and Server do not overlap. The
[CLI reference](https://docs.superdurable.io/reference/cli/) covers its TLS
and credential flags.

## Worker and access topology

Every Go Worker is a pull Worker. It opens only outbound connections to Dex
Server, and the Server never calls in. Set the Server address on both the
Worker and the Client. There is no Worker address to configure. Give each
process start a unique app Worker ID. The Dex SDK skill holds the Worker
lifecycle rules: startup registration, readiness, capacity, drain on stop,
and heartbeats. The [Go Worker reference](https://docs.superdurable.io/reference/go/workers)
and the [Server Worker reference](https://docs.superdurable.io/reference/server/workers)
hold the options and defaults.

When Dex Server enables authentication, configure a credential separately on
the Go Client, the Go Worker, `dexcli`, and Dex Web. Dex Web's FlowService
credential does not authenticate Web users, so protect the Web port
separately. The
[Server authentication reference](https://docs.superdurable.io/reference/server/authentication)
lists the credential kinds and flags.
