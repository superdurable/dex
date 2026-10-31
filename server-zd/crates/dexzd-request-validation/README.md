# dexzd-request-validation

## Purpose

Validate untrusted Dex client requests and Worker responses before state changes.

## Optional indexed Keyword limit

The `ValidationConfig` supplied when constructing the engine and effect runner includes `keyword_value_limit: Option<KeywordValueLimit>`. `KeywordValueLimit::postgres()` limits indexed Keyword values and each decoded KeywordArray element to 1024 UTF-8 bytes. Only writes with `IndexConfig.enable` set are checked. Unspecified types use the same inference function as the search projection. Text indexes and non-indexed values are unaffected.

Validation covers StartFlow initial Attributes, SetAttributes, Worker WaitFor and Execute results, SubFlow initial Attributes, and Worker RPC results. Client rejections return `INVALID_ARGUMENT` with `INDEXED_KEYWORD_TOO_LONG` before a write command is proposed. Step results arrive over the bidirectional PollWorkerTask stream, which has no completion acknowledgment. Oversized Step results produce a non-retryable attempt failure before completion is proposed; the SDK receives no per-result RPC error and does not retry the result. Existing Step failure policies apply: default policies fail the Flow, WaitFor's proceed policy enters Execute, and Execute's configured failure target starts its recovery Step. Failure details retain the status and sub-status.

The default is `None`, and server-zd explicitly wires `None` for every search backend. No deployed admission behavior changes until a backend enables the limit.
