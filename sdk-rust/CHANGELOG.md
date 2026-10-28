# Rust SDK changelog

## Unreleased

- **Breaking change:** Removed push Workers and Worker target/listener APIs. StartFlow and SubFlow now always select pull Workers, overriding server defaults or parent routing. UpdateFlowConfig leaves the Worker target unset and preserves the existing Flow target. See the [release notes](RELEASE_NOTES.md#unreleased) for removed APIs and migration instructions.
