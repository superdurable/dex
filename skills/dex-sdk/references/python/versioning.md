# Versioning Python applications

## Check first

```bash
python -c 'import importlib.metadata; print(importlib.metadata.version("dex-python-sdk"))'
uv tree | grep -i dex
```

Use the package name recorded by the project's lockfile if distribution metadata differs. Installed source/declared dependency is authoritative; this handbook is pinned evidence.

## Server protocol compatibility

Python Workers read their diagnostic version from distribution metadata. Both sync and async Worker startup call `GetServerInfo`, negotiate the highest common protocol, synchronize Attribute indexes, and then open their pull task streams. A missing RPC, invalid interval, or disjoint interval fails before any task stream opens. Upgrade a legacy Server first, and stop running Workers before a breaking Server release because they do not renegotiate.

## Open Flows

Preserve Flow type, Step type names, schema names/types, and decodable models for runs that can resume on new code. Renaming a Python class can change its Step identity. Add a new Step/Flow for incompatible behavior and keep old definitions registered until runs drain.

Dataclass field additions with defaults are safer than removals/type changes. Never repurpose string/enum meanings. Keep module import paths available if registry/bootstrap imports them.

## Rollout and policies

Deploy a Worker capable of old/new runs before replacing the previous Workers. Choose ID reuse and ignore-already-started as business semantics. Test active and closed prior IDs.

UpdateFlowConfig changes only the supplied Flow configuration fields. From Python SDK v1.25.0 it does not change a Flow's Worker routing; every Flow routes to pull Workers. Roll out new Worker code by deploying pull Workers that can serve both old and new runs.

Upgrade-test open runs across Worker replacement at map, Channel, Timer, RPC, retry, and timeout-handler boundaries. Run both async and sync surfaces if both are deployed.
