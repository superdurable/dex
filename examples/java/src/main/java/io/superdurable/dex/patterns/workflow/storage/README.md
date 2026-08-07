# Storage Pattern Implementation

This package demonstrates a singleton flow that acts as a small persistent
key-value store via RPC.

## Key Components

1. **StorageFlow**: Long-running singleton flow; RPCs add/get/remove items.
2. **`AttributeMap<String>`**: One Dex attribute instance per key (not one blob for
   the whole map).
3. **AddStorageItemRequest**: Request body for add.

## API Endpoints

- `POST /design-pattern/storage/add` — add or overwrite a key
- `GET /design-pattern/storage/get` — get a key (null if missing)
- `POST /design-pattern/storage/remove` — delete a key

## Why AttributeMap

Storing a `Map` inside a single `Attribute` rewrites the entire map on every
update and serializes all keys under one lock. `AttributeMap` keeps each key as
its own persistence entry, so updates are smaller and different keys do not
contend. Per-key `set` / `delete` need no whole-store RPC lock.

Each physical attribute still has a size limit (on the order of a few MB); this
pattern is for small KV state, not a general database.

## Usage notes

- Controller auto-starts the singleton flow if it is not running.
- Prefer search attributes when you need many non-singleton storage flows to be
  queryable.
