# TypeScript versioning

Open Flows may call a newly deployed Worker. Treat stable names and wire shapes as persisted interfaces: `getFlowType()`, `getStepType()`, Attribute/Channel/Stream names, RPC names, codec behavior, DTO fields, and Step graph decisions.

## Server protocol compatibility

TypeScript builds embed the diagnostic package version. Worker startup calls `GetServerInfo`, negotiates the highest common protocol, synchronizes Attribute indexes, and then opens its pull task streams. A missing RPC, invalid interval, or disjoint interval fails before any task stream opens. Upgrade a legacy Server first, and stop running Workers before a breaking Server release because they do not renegotiate.

## Before a rollout

1. Read the resolved `@superdurable/dex` version and lockfile.
2. Verify precise APIs against that release.
3. Inventory open Flows by type and Worker version.
4. Test an old-open Flow against the new Worker.
5. Decide how old and new behavior coexist and when old code can be removed.

Use additive fields with tolerant decoders where semantics allow. A compile-time optional property is not automatically wire-compatible. Renaming a Step class is harmless only if `getStepType()` remains stable and its registration/behavior remains compatible.

## Safe strategies

- Keep old Step and RPC implementations registered until open executions drain.
- Introduce a new Flow type for incompatible graphs.
- Persist a business version Attribute at start and branch on that durable value.
- Route only new starts to the new version.
- Remove old code only after no running, waiting, retrying, or recoverable execution needs it.

A Flow retry creates a new run and may receive a new timeout budget. It does not transform application state for you. server-zd rejects a Flow-level retry policy with INVALID_ARGUMENT, so this applies only to restarts the application performs.

See the canonical [Application Operations versioning section](https://docs.superdurable.io/production/application-operations#versioning-flow-code).
