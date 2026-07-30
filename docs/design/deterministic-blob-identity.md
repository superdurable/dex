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

## Blob UUID

Blob object names use deterministic UUIDv8:

```text
digest = SHA-256(lengthPrefixed(
  "dex-blob-v1",
  invocationID,
  payload,
))
blobUUID = UUIDv8(digest[0:16])
```

Each component is prefixed with its unsigned 64-bit big-endian byte length. The
first 16 digest bytes are copied before setting the UUIDv8 version and RFC
variant bits.

The complete path remains:

```text
yyyyMMdd$flowID/<blobUUID>
```

Activity writes use the workflow run ID plus activity ID as `invocationID`.
External API writes use the caller-provided request ID. Activity attempt numbers
are excluded.

The date prefix uses the server's UTC date when the object is written. A retry
crossing a UTC date boundary can create another path.

S3 bucket versioning must remain disabled. Otherwise, repeated writes to one
deterministic key retain hidden object versions.
