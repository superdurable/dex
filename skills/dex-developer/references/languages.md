# Language routing

Match the language and exact SDK version already used by the project. For new projects, resolve the current stable version from the official package registry or Dex documentation.

| Language | Package | Runnable examples | Common verification |
| --- | --- | --- | --- |
| Python | **dex-python-sdk** | **examples/python/dex_examples/** | **mypy**, **pytest**, project integration script |
| Go | **github.com/superdurable/dex/sdk-go** | **examples/go/** | **go test** through the project Makefile, example E2E script |
| Java | **io.superdurable:dex-sdk** | **examples/java/** | Gradle compile/test, example integration script |
| TypeScript | **@superdurable/dex** | **examples/typescript/** | **tsc**, Node tests, example integration script |
| Rust | **dex-sdk** | **examples/rust/** | **cargo fmt**, **cargo clippy**, **cargo test**, example integration script |

## Selection workflow

1. Read the dependency manifest and lockfile.
2. Find the nearest primitive or product example in the same language.
3. Confirm imported names and signatures against the installed SDK source.
4. Follow the application's existing registry, Worker, Client, codec, and error-handling conventions.
5. Run formatting and static checks before integration tests.

## Language-specific cautions

### Python

Preserve whether the application uses the synchronous or asynchronous SDK style. Do not mix them in one execution path. Use typed models and the project's configured codec behavior.

### Go

Use typed definitions and return every SDK error. Follow the repository's constructor and interface conventions. Prefer project Makefile targets for full suites.

### Java

Keep Flow, Step, and RPC types explicit. Use the project's dependency injection and lifecycle conventions without making Worker-managed definitions request-scoped.

### TypeScript

Provide codecs for typed values where required by the installed SDK. Preserve async boundaries and await Client or handler operations supported by that version.

### Rust

Follow the installed SDK's ownership model. In the Dex repository examples and SDK integration tests, define Attribute, Channel, and Stream schemas as module-level **LazyLock** statics when that convention applies.

## Version discipline

Package versions across languages may be released at different times. Do not infer that equal-looking version numbers expose identical APIs. Use language-native examples from the same release whenever possible.
