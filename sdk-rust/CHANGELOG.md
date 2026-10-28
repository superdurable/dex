# Rust SDK changelog

## Unreleased

- **Breaking change:** Removed push Workers and Worker target/listener APIs. StartFlow, SubFlow, and UpdateFlowConfig now always select pull Workers, overriding server defaults, parent routing, or an existing push target. See the [release notes](RELEASE_NOTES.md#unreleased) for removed APIs and migration instructions.
