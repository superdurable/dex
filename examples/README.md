# Examples

Language-specific sample applications for Dex:

| Path | Language |
|------|----------|
| [go/](go/) | Go |
| [java/](java/) | Java |
| [python/](python/) | Python |
| [rust/](rust/) | Rust |
| [typescript/](typescript/) | TypeScript |

Each tree has its own README with run instructions. CI workflows are `.github/workflows/examples-*-ci.yml`.

The shared [Entity Store setup](entity-store/) adds PostgreSQL for the user-profile
projection example implemented by all five language applications.
