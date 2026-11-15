# examples/rust Rules

Read `.cursor/rules/rust.mdc` (Rust Code Conventions, R1–R19) before changing
Rust code here. The repository root `AGENTS.md` rules also apply.

### Rust Workflow Schema Definitions

In Rust examples and SDK tests under `sdk-rust/crates/dex-sdk/tests/integ/`,
define every Attribute, Channel, and Stream as a module-level `static
LazyLock<T>`. Do not use a function to construct or return one of these
definitions. Reuse the static directly, or clone its initialized value only
when an owned field is required.
