# dexzd-keyspace

## Purpose

Identifiers, partition hashing, RocksDB key encodings, and value references shared by every server-zd crate.

## Local search-pending sidecar

SQL visibility uses a separate RocksDB directory named by appending `.search-pending`
to `localStorage.directory`. It is local projection metadata, outside engine snapshots,
replicated state, and `dexzd-apply`. The engine key layout version is unchanged.

Keys live in the sidecar's `NodeMeta` column family. Their namespace is ASCII
`search-pending/v1/` followed by the 16-byte cluster ID. Numeric suffixes use big endian:

| Suffix | Value |
| --- | --- |
| `e` + partition:u16 + tenant:u64 + run:16 bytes | Private protobuf pending record: tenant, run, Flow hash, priority, revision. |
| `o` + priority:u8 + partition:u16 + tenant:u64 + run:16 bytes | Primary entry key, ordering starts before closes before intermediates; reconciliation Deletes follow. |
| `m` + partition:u16 | One-byte rebuild-needed reason mask, cleared after a successful manual rebuild. |

Pending record tags are tenant 1, run 2, Flow hash 3, priority 4, and revision 5.
Priority values are start 0, close 1, intermediate 2, and reconciliation Delete 3.
A revision check preserves a new drop recorded while an older SQL repair is in flight.
Primary and priority entries change in one local batch. A local write alone never
allows SQL progress past a shed change; the object's successful sync or an already
committed SQL repair is required. Startup version comparison covers unsynced crash gaps.
