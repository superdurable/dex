# Channel Message Queue

## Scope

This design adds stable identities and pending-message management to Channels.
It changes the unpublished protocol directly. There is no compatibility layer.

## Message identity

Every pending **ChannelMessage** contains a UUIDv7 **message_id**. Dex replaces
IDs supplied through public or Worker protocol payloads at the API or Activity
boundary. Workflow code therefore receives an already assigned ID and remains
deterministic.

Message IDs identify queued instances, not values. Dex does not enforce ID
uniqueness. If trusted stored state contains duplicate IDs, deletion removes
the first FIFO match.

The Channel Store persists complete message envelopes. A Step Channel condition
still returns only each message's **Value**. Continue-as-New snapshots, Flow
state, semantic history, reset reapplication, and blob hydration retain the ID.

## Read and delete APIs

**GetChannelMessages** returns every pending message for one Channel in FIFO
order. It does not paginate. Consumed messages are no longer returned.

**DeleteChannelMessage** targets a Channel name and message ID. Temporal executes
the deletion as a synchronous Update and returns NotFound with
**CHANNEL_MESSAGE_NOT_FOUND** when the pending message does not exist.

Cadence cannot execute synchronous Updates. Dex first queries the Channel and
then sends a signal. This detects messages missing at query time, but consumption
can race the signal. A missing message at signal application is a no-op.

## RPC side effects

**InvokeWorkerRPCResponse** can stage Channel deletions alongside Attribute
writes, Channel publications, events, and a Step decision. On Temporal, an
**InvokeRPCRequest** with **is_transactional** executes its reads and writes
through one synchronous Update. Dex verifies every staged deletion before
committing any Flow mutation. One missing message fails the RPC and commits none
of its side effects.

Attribute locking enables transactional execution automatically. A staged
Channel deletion does not; the caller must explicitly set **is_transactional**
when deletion existence and the other side effects must commit atomically.

Signal-based RPCs keep best-effort semantics. Missing deletions are no-ops and
all other valid side effects continue. Cadence always uses this path, including
when **is_transactional** is requested, and does not provide transactional
guarantees.

Deletions are applied before publications. Moving a message therefore deletes
its old queue instance and publishes a new instance with a new server-generated
UUIDv7.

## History and reset

External deletion has its own semantic history payload. RPC history includes
staged deletions. Temporal successful Update history and Cadence signal history
both project the same deletion metadata.

Reset reapplication preserves server-assigned IDs from the original signal or
Update. Continue-as-New serializes the complete pending message envelope.

## Tests

- Integration: UUIDv7 assignment, FIFO listing, consumption, deletion, NotFound,
  duplicate identities, ChannelMap, Continue-as-New, reset, and large Values.
- Temporal integration: synchronous RPC deletion validation commits no partial
  Attribute, publication, event, or Step-decision side effects.
- Temporal and Cadence integration: signal-based missing deletion remains a
  no-op while other side effects commit.
- Cadence integration: direct delete follows the query-plus-signal path and
  reports IDs missing at query time.

## Documentation

Update the proto, server, and server integration READMEs in this phase. SDK and
product documentation belongs to the later client and example phases.

## UI/UX

N/A: this phase changes only the protocol and server.
