# RPC selective state loading

## Goal

Worker RPC handlers should receive the state they need without copying every
potentially large collection into every invocation. The protocol keeps small,
frequently used metadata available while requiring callers to select collection
contents explicitly.

## Request selectors

`InvokeRPCRequest` has three collection selectors:

- `load_attribute_map_names` loads every current entry for each selected
  AttributeMap definition.
- `load_channel_names` loads pending envelopes for each selected Channel
  definition.
- `load_channel_map_names` loads pending envelopes for every current instance
  of each selected ChannelMap definition.

Each selector is a definition name. Names must be non-empty, unique within their
selector, and cannot contain `/`. Dex sorts selectors before preparing the
Worker request. Per-instance ChannelMap selection, pagination, and silent
truncation are intentionally unsupported.

## Worker state projection

Every `InvokeWorkerRPCRequest` contains:

- all ordinary Attribute values;
- entries from explicitly selected AttributeMap definitions;
- `channel_infos` for every known Channel and ChannelMap instance;
- `loaded_channel_messages` for explicitly selected Channel and ChannelMap
  definitions; and
- the normalized selector names.

ChannelInfo contains only queue size metadata. Reading Channel size or
ChannelMap keys and sizes does not require loading pending messages.

The echoed selector names distinguish a loaded collection with no entries from
a collection that was not loaded. An explicitly selected empty Channel has an
empty `ChannelValues` entry. An empty selected ChannelMap is represented by its
echoed selector because it has no physical instances.

Pending envelopes preserve FIFO order, server-generated message ID, and Value.
Loading is a snapshot operation and does not consume messages. Large Values use
the same eager or lazy Blob Store policy as other Worker RPC state.

## Atomicity and isolation

Loading, transactional execution, and Attribute locking solve different
problems:

- Loading selects the collection data sent to the Worker.
- Transactional execution commits all staged effects together and verifies that
  every staged Channel deletion still identifies a pending message.
- Attribute locking isolates cooperating Steps and RPCs that use the same lock.

A transactional handler can load a message, stage its deletion by ID, and
publish its Value elsewhere. If another operation consumes the message before
commit, deletion validation fails and none of the staged effects commit. This
supports ID-only move operations without a long-held lock.

Transactional execution alone does not isolate decisions based on an entire
Channel, ChannelMap, or AttributeMap snapshot. When an application requires
that isolation, every cooperating Step and RPC writer must acquire the same
Attribute lock. Attribute locking implicitly enables transactional execution,
but it does not implicitly load AttributeMap entries or Channel messages.

Cadence retains its best-effort behavior and does not promise the same atomic
commit guarantees.

## Persistence and history

The selective projection is derived from durable Flow state. Continue-as-New,
reset, and replay preserve the underlying AttributeMap entries and pending
message envelopes, including message IDs and Values. Preparing a snapshot does
not create a Dex semantic history event.

## Failure behavior

Invalid selectors fail before Worker dispatch. Lazy Blob Store references remain
available for Worker-side hydration. Eager loading hydrates selected pending
message Values before dispatch and fails the RPC if hydration fails.

Transactional deletion validation uses the existing Channel-message-not-found
error. A failed validation commits no Attribute writes, Channel publications,
events, deletions, or Step decisions.
