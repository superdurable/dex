---
name: dex-developer
description: Develop, debug, test, and operate applications built with Superdurable Dex in Python, Go, Java, TypeScript, or Rust. Use when a user mentions Dex Flows, Steps, Attributes, Channels, Streams, RPCs, SubFlows, Workers, Dex Web, Dex Server, or dexcli. Do not activate for unrelated uses of the word "dex" or for Dex framework/server implementation work.
---

# Dex Developer

Build reliable applications through Dex's public programming model. Keep the user's experience entirely in Dex terms and APIs.

## Stay at the Dex boundary

- Model application behavior with Flows, Steps, Waits, Attributes, Channels, Streams, RPCs, Timers, SubFlows, Workers, and the Client.
- Use Dex Web, dexcli, SDK errors, and application logs for inspection and recovery.
- Do not require users to understand, install, configure, or inspect the execution engine inside Dex Server.
- Do not expose internal server concepts as application requirements. Only discuss internals when the user explicitly asks to develop or debug Dex itself.

## Establish the project context

Before writing code:

1. Identify the language, package manager, installed Dex SDK version, existing Flow registry, Worker bootstrap, Client bootstrap, and test commands.
2. Preserve the project's SDK version unless the user asks to upgrade. Do not mix APIs copied from another version.
3. Prefer the project's existing Dex code and conventions. In a Dex source checkout, use runnable files under **examples/** as the API source of truth.
4. For a new project, use the current official docs at https://docs.superdurable.io and the matching language examples at https://github.com/superdurable/dex/tree/main/examples.
5. Never invent Dex APIs. If an exact signature is uncertain, inspect the installed SDK or a version-matched example before coding.

Read [getting-started.md](references/getting-started.md) when installing Dex, creating the first Flow, or wiring a Worker and Client. Read [languages.md](references/languages.md) for language-specific package names, source locations, and verification commands.

## Model the application first

Write a short application-level design before a non-trivial implementation:

- Flow type and business identity
- start input and completion output
- Step types and transitions
- durable state held in Attributes
- durable commands or events carried by Channels
- synchronous interactions exposed as RPCs
- low-latency, best-effort updates exposed as Streams
- timers, retries, timeouts, and failure paths
- SubFlow boundaries and concurrency limits

Keep external side effects in **Execute**. Use **WaitFor** to declare durable Conditions and prepare state needed for the wait. Make every Step transition and terminal decision explicit.

Read [modeling.md](references/modeling.md) for design rules and [primitives.md](references/primitives.md) when choosing or combining primitives.

Read [large-attributes-and-locality.md](references/large-attributes-and-locality.md) when a Flow keeps large documents, conversation history, or API/MCP results in Attributes; when choosing AttributeMap instances or external projections for a growing collection; or when deploying replicated Workers. Do not add an application-managed blob store, cache, or dual-write path solely because an Attribute is large; first evaluate Dex blob hydration, the SDK BlobCache, headless Worker locality, and Attribute Store synchronization.

## Choose proven Flow shapes

Prefer an existing Dex pattern over an ad hoc coordination loop. Read [patterns.md](references/patterns.md) when the request involves retries with compensation, polling, reminders, inactivity, parallel work, fan-out, back pressure, interruption, external publishing, responsive updates, or durable entity state.

Adapt the nearest runnable example to the project's language and SDK version. Preserve the example's Flow shape while replacing only the application domain and integrations.

## Implement a complete vertical slice

For application changes, include the pieces needed to run and exercise the behavior:

- typed inputs, outputs, and application errors
- Flow and Step definitions
- persistence schema entries for every used Attribute and Channel
- constructor-injected application dependencies
- Flow registry entry
- Worker and Client wiring when not already present
- an application boundary such as an HTTP handler, CLI command, or service method
- an integration test that starts and interacts with a real Flow

Do not add global mutable dependencies or post-construction setter injection. Keep stable Flow, Step, Attribute, Channel, Stream, and RPC names compatible with open executions unless the user explicitly plans a migration.

## Verify and operate

Use the repository's own build and test entrypoints. Prefer an integration test against **dexcli dev** for behavior that crosses Worker, Client, persistence, waiting, retry, or RPC boundaries. Use unique Flow IDs and poll for convergence instead of fixed sleeps.

For diagnosis, begin with read-only evidence: application logs, Dex Web, and **dexcli flow inspect**. Do not stop, time travel, publish, invoke, or mutate a Flow unless the user requested that action. Resolve the exact Flow and run before any mutation.

Read [operations.md](references/operations.md) for test scenarios, diagnostic order, safe recovery, and versioning constraints.

## Completion criteria

Before handing off application code:

- build or type-check succeeds
- the relevant integration scenario passes
- every used durable primitive is registered
- retry, timeout, duplicate request, and terminal behavior are intentional
- Worker and Client share compatible registry and payload/blob configuration
- user-facing instructions mention only Dex components and commands
