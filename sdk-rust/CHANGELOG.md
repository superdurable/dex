# Rust SDK changelog

## Unreleased

- Add `FlowConfig::delete_expired_history_objects` to control expired history object
  deletion at start, on update, and for SubFlows. Omission uses the server default at
  start and inherits for children; explicit false is preserved. Requires history
  offload. See the API reference for deletion timing and update semantics.
  Requires server-zd 0.1.22 or later; older servers ignore the value.

- Fixed hydration of more than 1000 values with history offload enabled by batching
  LoadBlobs requests in input order.

- Added `FlowConfig::history_window_events`, which sets about how many of the newest history events a run keeps for history reads and Time Travel. Set it at start through `StartFlowOptions::config_override`, change it with `Client::update_flow_config`, or override it for a SubFlow; a SubFlow otherwise inherits its parent's value. Omitting it uses the server's `history.windowEvents` setting (10000 by default). Dex rejects zero or a value above the server's `history.maxWindowEvents` setting (100000 by default) with `INVALID_ARGUMENT`; a value above `i32::MAX` fails locally with `SdkError::InvalidArgument`. Requires server-zd 0.1.21 or later; older servers ignore the value.

- Added `FlowServiceTlsOptions` for verified TLS 1.2+ Client and pull Worker connections, with native system roots, additional PEM roots, a server name override, and required ALPN h2. Configure it with `FlowServiceTlsOptions::new().additional_root_cas_pem(bytes).server_name(name)`. Additional roots work without native system CAs; partial native-load failures and skipped malformed certificate blocks emit count-only warnings. Empty trust stores construct successfully and fail certificate verification at connection time. Plaintext remains the default.

- **Breaking change:** Removed push Workers and Worker target/listener APIs. StartFlow and SubFlow now always select pull Workers, overriding server defaults or parent routing. UpdateFlowConfig leaves the Worker target unset and preserves the existing Flow target. See the [release notes](RELEASE_NOTES.md#unreleased) for removed APIs and migration instructions.
- **Breaking change:** `dex-protocol` drops the Go Server internal protocol types and the removed push Worker fields. See the [release notes](RELEASE_NOTES.md#unreleased).
- Added `Client::get_attributes`, which reads committed Attribute and AttributeMap instance values from the current or latest Flow run in one server request, without a Worker. Pass a tuple of 1 to 12 reads, such as `client.get_attributes(flow_id, (&ORDER_STATUS, &NOTES.instance("n1")))?`, and receive a tuple of `Option<T>`. Added `AttributeMap::instance`, which returns the new `AttributeMapInstance<T>` selector, and the sealed `AttributeRead` and `AttributeReads` traits. Every read must match a definition registered in the Client's `Registry`, including its Rust value type. Hard-delete Attributes are readable, and large values are resolved through the blob cache.
