# Storage Flow

Uses a flow as a small durable key-value store. The flow has no steps at all —
only RPCs that read and write an attribute map — so it stays alive without
consuming actions and serves reads directly from workflow memory.

## Use cases

- Configuration or feature-flag state that many flows read.
- A per-entity cache that must survive process restarts.
- Coordination state that several callers mutate through RPCs.

## RPCs

- `add_item` — stores a value under a key, taking an `AddStorageItemRequest`.
- `get_item` — returns the value for a key.
- `remove_item` — deletes a key.

## Persistence

- `Store` — an `AttributeMap` of `str` values holding the key-value pairs.

## Note on step-less flows

`get_steps` returns `StepList.empty()`, so nothing runs when the flow starts. It
stays alive until it is explicitly stopped or its timeout passes, and every
operation happens through RPCs.
