# Stage 0: workspace, language, and local tooling

Inspect the repository before you select a language, create manifests, or
install dependencies. A repository is effectively empty when it has no
application source or build manifests. A README, license, editor
configuration, or other placeholder does not count.

The installed Dex Skills release that the coding-agent host loads is the only
skill authority. Never assume a fixed skill filesystem path. Never add a skill
submodule or a floating skill branch to the application repository.

## Choose the language

App Builder supports Go, Python, TypeScript, Java, and Rust equally. Pick the
backend language in this order:

1. An existing application keeps its language and toolchain.
2. Otherwise, use the language or team stack the user names.
3. Otherwise, use TypeScript. Say once, in the first implementation update,
   that the application uses TypeScript and that another language is fine.

Every later stage is the same for each language. Load the SDK
reference for that language, and copy API shapes from that language's
runnable examples. For Dex Web metadata, use only the `dex:` directives and
options that the
[Flow visualization reference](https://docs.superdurable.io/reference/web/flow-visualization)
lists for the language. Skip a capability it does not list. Never invent
syntax, and never ask the user to switch languages for it.

## Existing application

Preserve the application's intentional structure, language, package
manifests, Dex SDK requirement, `dexcli` expectation, lockfiles, generators,
and commands. Reuse its declared build, generation, and run commands instead
of inventing parallel ones. Change a dependency only when the user asks or a
recorded compatibility rule requires it. Explain the verification consequence
first.

## Effectively empty repository

Create an application in the chosen language that depends only on
source-available Dex:

1. Install `dexcli` with the supported package for the platform. With
   Homebrew, on macOS or Linux, that is
   `brew install superdurable/tap/dexcli`. Run `dexcli version`.
2. Create the project with the language's own package manager. Add the Dex
   SDK for that language at an exact released version. The SDK
   language reference and the SDK's Developer reference name the package.
   Choose the newest stable SDK release that pairs with the installed
   `dexcli` and the target Server. The
   [Developer reference](https://docs.superdurable.io/reference/) has the
   compatibility matrix. Never copy versions from a neighboring project, and
   never resolve a branch, pseudo-version, or commit.
3. Follow the [SDK reference routing](../SKILL.md#load-references) for Core and
   language references for the Registry, Worker, Client, and first vertical
   slice. Copy API shapes from the runnable examples for that language:
   [Go](https://github.com/superdurable/dex/tree/main/examples/go),
   [Python](https://github.com/superdurable/dex/tree/main/examples/python),
   [TypeScript](https://github.com/superdurable/dex/tree/main/examples/typescript),
   [Java](https://github.com/superdurable/dex/tree/main/examples/java), or
   [Rust](https://github.com/superdurable/dex/tree/main/examples/rust).
4. Keep each Flow in its own source file named for its domain. Add one
   Registry that composes every Flow and one Worker entry point. Add an
   application HTTP server only for a confirmed custom UI or integration
   ingress. Add a frontend directory only for a confirmed custom UI.
5. Record the project's own commands for FDG rendering, code generation,
   checks, builds, tests, and local runs in its README or build file. Later
   work reuses them.

In the first implementation update, name the selected language, Dex SDK
release, and `dexcli` release. If a release cannot be resolved or verified,
report that blocker. Never fall back to an unreleased dependency.

An optional frontend and its generated client stay separate from the
backend, whatever its language. Use an official Connector package that the
catalog lists for the application's language.

## Pair the SDK and the CLI

Project scripts and FDG rendering call the `dexcli` on `PATH`. Run
`dexcli version` first. The FDG analyzer must name Flow and Step types the same
way the Worker's SDK registers them. Pair `dexcli` with the project's SDK
release so the rendered graph matches the Worker. When the graph and Worker
names differ, fix the pairing. Do not add Flow or Step type-name overrides as
a workaround.

When upgrading the global CLI would break other projects, install a
project-local `dexcli` and put its directory first on `PATH` for this project.

Before a Worker connects to a non-local Dex Server, run
`dexcli version check --server ADDRESS` against that Server. It reports the
highest common protocol and fails when the SDK and Server do not overlap. The
[CLI reference](https://docs.superdurable.io/reference/cli/) covers its TLS
and credential flags.

## Worker and access topology

Every Worker is a pull Worker. It opens only outbound connections to Dex
Server, and the Server never calls in. Set the Server address on both the
Worker and the Client. There is no Worker address to configure. Give each
process start a unique app Worker ID. [application.md](core/application.md) holds the Worker
lifecycle rules: startup registration, readiness, capacity, drain on stop,
and heartbeats. The Workers page of the language's SDK reference, such as the
[Go Worker reference](https://docs.superdurable.io/reference/go/workers),
and the [Server Worker reference](https://docs.superdurable.io/reference/server/workers)
hold the options and defaults.

When Dex Server enables authentication, configure a credential separately on
the Client, the Worker, `dexcli`, and Dex Web. Dex Web's FlowService
credential does not authenticate Web users, so protect the Web port
separately. The
[Server authentication reference](https://docs.superdurable.io/reference/server/authentication)
lists the credential kinds and flags.
