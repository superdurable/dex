# dexzd-cluster-metadata

## Purpose

Cluster object, partition manifest, and node leases that decide which server-zd node owns each partition.

## Startup sequence

Call `bootstrap_or_validate` before using a cluster. It creates the immutable cluster object once and checks partition count, hash algorithm, format version, and key layout on later starts. The returned partition hasher uses the stored key.

Create the initial manifest with `create_initial_manifest`; existing nodes read it through `read_manifest`. Call `acquire_lease` before writing the node's log stream. It acquires the lease and bumps the node epoch before returning. While another process holds the lease, it rereads the lease every `LeaseConfig::held_lease_poll_limit` (one second by default), or every third of the lease duration if that is shorter. Each wait is randomized down to half of that interval, so several waiting processes do not read in step. It takes a released lease at once and takes an expired one only after the takeover grace. With `LeaseConfig::acquisition_delay` set, it first waits the delay that the provider returns once for each released or expired lease version, then reads the lease again, so a readier process can take it first; only the conditional lease write decides who acquires. `read_node_lease` reads the lease without competing for it. Read the manifest that recovery depends on after `acquire_lease` returns; the previous holder can change it until then. Keep the `LeaseHandle` alive while serving requests and watch `LeaseStatus`. Stop serving immediately when it reports `Lost` or the `ValidUntil` deadline passes. During orderly shutdown, stop serving before calling `shutdown`; subscribers see `Lost` before the lease is released for immediate reacquisition. `shutdown` returns `LeaseReleaseError` when object storage rejects the release or the release does not finish within `LeaseHandle::RELEASE_BUDGET`; the lease then stays held until it expires. It returns `Ok` when another process already took the lease over. Dropping the handle only stops renewal; the lease then expires normally. An `acquire_lease` that fails or is cancelled after writing the lease releases it before the error returns, or in the background after cancellation.

Manifest changes use `update_manifest` and ETag comparison. `raise_version_barrier` and `update_partition_snapshot` apply the common updates. `register_attribute_indexes` adds tenant attribute index declarations and returns `AttributeIndexConflict` if an existing key changes type.

## Embedding application identity and version barrier

An application that embeds server-zd and keeps its own state in the cluster supplies `Some(EmbeddingApplication)` in `ClusterConfig.embedding_application`. server-zd itself uses `None`. The immutable cluster object records the application's `name` and `key_layout_version` at creation. A node opens it only when both match its configuration, so server-zd and embedding applications never open each other's clusters.

`VersionBarrier.application_version` in the manifest records the creating application's apply version. Every log record after each partition's snapshot uses that version. The value is 0 when there is no embedding application; server-zd clusters always record 0. The engine checks that the value lies in `supported_versions` before acquiring the lease and again in `ClusterMetadata::read_under_lease`. A node starts only when both checks pass.

The application version changes only in the same manifest update as snapshot references covering every record of the previous version. Changing the barrier alone does not establish this condition.

## Optional Attribute index capacity

Pass `Some(&AttributeIndexCapacity::postgres())` to `register_attribute_indexes` to limit each tenant to ten Keyword keys and three keys per other supported type. The CAS loop checks only the requesting tenant and types receiving new keys before writing. Re-registering existing keys succeeds even when that tenant is already over capacity. Other tenants and unchanged types do not affect registration. Capacity rejection includes the requesting tenant, type, limit, previously registered keys, and rejected new keys. Registrations are append-only; there is no delete operation.

Call `validate_attribute_index_capacity` on a loaded registry before enabling a backend with fixed slots. It returns the same typed error for existing excess occupancy. Passing `None` disables capacity checks; server-zd wires `Some(AttributeIndexCapacity::postgres())` for the `postgres` search backend, enabling a per-tenant capacity of ten Keyword keys and three keys per other supported type, with system fields excluded. The `builtin` and ParadeDB search backends use `None`.
