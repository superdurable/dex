# Deterministic blob identity

This document defines the SDK/server protocol used to make external blob writes
retry-safe. It is not an end-user API contract.

## Request identity

The following top-level request fields are required:

- `StartFlowRequest.request_id`
- `SetAttributesRequest.request_id`
- `InvokeRPCRequest.request_id`

The SDK must generate one request ID and reuse it for every retry of the same
request. Request IDs deduplicate blob writes.

For `StartFlow`, the server stores the request ID in workflow memo. When
`ignore_already_started_error` is enabled, an AlreadyStarted error is ignored
only when the running workflow has the same request ID.

`FlowAlreadyStartedOptions.request_id` is removed and its field number and name
are reserved.

## Blob object ID

Blob object names use a configurable deterministic lowercase Base36 ID:

```text
digest = SHA-256(lengthPrefixed(
  "dex-blob-v2",
  invocationID,
  payload,
))
objectID = fixedWidthBase36(digest mod 36^objectIdLength)
```

Each component is prefixed with its unsigned 64-bit big-endian byte length. The
alphabet is `0123456789abcdefghijklmnopqrstuvwxyz`. The configured length defaults
to 10 and accepts 10 through 50. All Servers writing the same namespace use the
same immutable value.

Durable History stores a compact locator without the Flow ID:

```text
<storageId>|<yyyyMMdd>/<objectID>[|<encoding>]
```

The Server combines the locator with the contextual Flow ID:

```text
<namespace>/v2/<yyyyMMdd>$<base64url(flowID)>/<objectID>
```

Activity writes use the workflow run ID plus activity ID as `invocationID`.
External API writes use the caller-provided request ID. Activity attempt numbers
are excluded.

Normal writes overwrite an existing key. There is no conditional create,
read-before-write, collision retry, or fallback key. This accepts the
probabilistic collision risk of the configured truncated hash.

Internal Blob references belong to one Flow. Before a reference crosses a Flow
boundary, the Server reads it with the source Flow ID and writes any still-large
value under the destination Flow ID. This makes cleanup of the source Flow safe.

The date prefix uses the server's UTC date when the object is written. A retry
crossing a UTC date boundary can create another path.

S3 bucket versioning must remain disabled. Otherwise, repeated writes to one
deterministic key retain hidden object versions.
