# Rust SDK changelog

## Unreleased

- Added `FlowServiceTlsOptions` for verified TLS 1.2+ Client and pull Worker connections, with native system roots, additional PEM roots, a server name override, and required ALPN h2. Plaintext remains the default.

- **Breaking change:** Removed push Workers and Worker target/listener APIs. StartFlow and SubFlow now always select pull Workers, overriding server defaults or parent routing. UpdateFlowConfig leaves the Worker target unset and preserves the existing Flow target. See the [release notes](RELEASE_NOTES.md#unreleased) for removed APIs and migration instructions.
- **Breaking change:** `dex-protocol` drops the Go Server internal protocol types and the removed push Worker fields. See the [release notes](RELEASE_NOTES.md#unreleased).
- Added `Client::get_attributes`, which reads committed Attribute and AttributeMap instance values from the current or latest Flow run in one server request, without a Worker. Pass a tuple of 1 to 12 reads, such as `client.get_attributes(flow_id, (&ORDER_STATUS, &NOTES.instance("n1")))?`, and receive a tuple of `Option<T>`. Added `AttributeMap::instance`, which returns the new `AttributeMapInstance<T>` selector, and the sealed `AttributeRead` and `AttributeReads` traits. Every read must match a definition registered in the Client's `Registry`, including its Rust value type. Hard-delete Attributes are readable, and large values are resolved through the blob cache.
