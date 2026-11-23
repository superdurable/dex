# Rust Flow analyzer

## Purpose

Parse Rust workflow source for `dexcli visualize`.

Dexcli embeds the crate sources and builds the analyzer in its user cache.
The analyzer reads a JSON request from stdin and writes analysis facts to stdout.
