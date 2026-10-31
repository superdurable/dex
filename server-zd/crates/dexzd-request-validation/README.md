# dexzd-request-validation

## Purpose

Validate untrusted Dex client requests and Worker responses before state changes.

## Optional indexed Keyword limit

The `ValidationConfig` supplied when constructing the engine and effect runner includes `keyword_value_limit: Option<KeywordValueLimit>`. `KeywordValueLimit::postgres()` limits indexed Keyword values and each decoded KeywordArray element to 1024 UTF-8 bytes. Only writes with `IndexConfig.enable` set are checked. Unspecified types use the same inference function as the search projection. Text indexes and non-indexed values are unaffected.

Validation covers StartFlow initial Attributes, SetAttributes, Worker WaitFor and Execute results, SubFlow initial Attributes, and Worker RPC results. Client rejections return `INVALID_ARGUMENT` with `INDEXED_KEYWORD_TOO_LONG` before a write command is proposed. Step results arrive over the bidirectional PollWorkerTask stream, which has no completion acknowledgment. Oversized Step results produce a non-retryable attempt failure before completion is proposed; the SDK receives no per-result RPC error and does not retry the result. Existing Step failure policies apply: default policies fail the Flow, WaitFor's proceed policy enters Execute, and Execute's configured failure target starts its recovery Step. These failures reuse the non-retryable `ValueTooLarge` attempt kind. Applications see `FLOW_ERROR_TYPE_WORKER_API_FAIL` and the message `indexed Attribute "status" has a Keyword value or KeywordArray element of 1025 bytes; maximum is 1024 bytes` for a 1025-byte value on `status`. Recovery details use error type `ValueTooLarge` and the same message.

The default is `None`, and server-zd wires `Some(KeywordValueLimit::postgres())` for the `postgres` search backend, enabling the 1024-byte limit for indexed Keyword values and each decoded KeywordArray element. The `builtin` and ParadeDB search backends use `None`.

The limit rejects new writes at ingress only when the search projection would index them. Payloads larger than 256 KiB and non-UTF-8 Keyword object payloads are accepted because the projection skips them. Stored values can still be re-indexed through search document rebuilds or time travel, including values committed before the limit was enabled. Search backends must tolerate or skip stored values that exceed their own limits.
