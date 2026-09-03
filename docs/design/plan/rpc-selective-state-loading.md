# RPC selective state loading

## Goal

Worker RPC handlers should receive the state they need without copying every
potentially large collection into every invocation. The protocol keeps small,
frequently used metadata available while requiring callers to select collection
contents explicitly.

## Request selectors

`InvokeRPCRequest` has three collection selectors:

- `load_attribute_map_selectors` loads every current entry with `AttributeMap/`
  or one entry with `AttributeMap/<instance>`.
- `load_channel_names` loads pending envelopes for each selected Channel
  definition.
- `load_channel_map_selectors` loads pending envelopes for every current instance
  with `ChannelMap/`, or one instance with `ChannelMap/<instance>`.

Singleton Channel selectors are definition names. Map selectors contain exactly
one `/`. A trailing separator selects all instances. The suffix selects one
instance. Slash is prohibited in instance keys because it is a reserved
character. Selectors must be non-empty and unique. Dex sorts them before
preparing the Worker request. Pagination and silent truncation are unsupported.

## SDK APIs

Each SDK exposes typed selections from the persistence definition itself. An
AttributeMap can load one logical instance or all instances. A Channel loads its
pending messages. A ChannelMap can load one logical instance or all instances.
SDK registries reject a selection whose definition is not registered in the
Flow schema, has the wrong persistence kind, or duplicates another selection.
Clients escape logical map instances and sort the resulting protocol selectors.

RPC Context APIs return typed FIFO message envelopes and support lookup by
server-assigned message ID. A selected empty collection returns an empty result.
Reading a collection that was not selected raises a stable state-not-loaded
usage error. Staged publications and deletions do not mutate the input snapshot.

## Worker state projection

Every `InvokeWorkerRPCRequest` contains:

- all ordinary Attribute values;
- entries from explicitly selected AttributeMap definitions;
- `channel_infos` for every known Channel and ChannelMap instance;
- `loaded_channel_messages` for explicitly selected Channel and ChannelMap
  definitions; and
- the validated, sorted selectors.

ChannelInfo contains only queue size metadata. Reading Channel size or
ChannelMap keys and sizes does not require loading pending messages.

The echoed selectors distinguish a loaded collection with no entries from a
collection that was not loaded. An explicitly selected empty Channel or exact
ChannelMap instance has an empty `ChannelValues` entry. An empty all-instance
ChannelMap selection is represented only by its echoed selector.

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
