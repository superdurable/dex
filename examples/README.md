# Examples

Language-specific sample applications for Dex. Each tree organizes Flows into
three categories:

| Category | Purpose | HTTP prefix |
|----------|---------|-------------|
| **products** | Real-world business scenarios | `/products/<kebab-name>/...` |
| **patterns** | Durable workflow design patterns | `/patterns/<kebab-name>/...` |
| **primitives** | One minimal example per Dex primitive | `/primitives/<kebab-name>/...` |

Languages with HTTP controllers expose routes under these prefixes. Cron schedule
and some Worker-only examples have no HTTP surface.

| Path | Language |
|------|----------|
| [go/](go/) | Go |
| [java/](java/) | Java |
| [python/](python/) | Python |
| [rust/](rust/) | Rust |
| [typescript/](typescript/) | TypeScript |

Each tree has its own README with run instructions. CI workflows are
`.github/workflows/examples-*-ci.yml`.

The shared [Entity Store setup](entity-store/) adds PostgreSQL for the
user-profile projection example implemented by all five language applications.
