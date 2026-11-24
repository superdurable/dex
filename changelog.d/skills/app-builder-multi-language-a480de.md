---
bump: patch
---
- **dex-app-builder** builds the backend in Go, Python, TypeScript, Java, or Rust. Stage 0 keeps an existing application's language, uses the language the user names, and otherwise uses TypeScript.
- **dex-app-builder** FDG authoring is language-neutral: use only the `dex:` directives and options that the Flow visualization reference lists for the application's language, and never invent syntax.
- **dex-app-builder** uses the Connector packages the catalog lists for the application's language, and ports n8n nodes to pure functions with tests in that language.
- **dex-sdk** language references apply the same directive rule for Python, TypeScript, Java, and Rust.
- Skills link examples that show a mechanism, not product examples under `examples/*/products/`. The five product-example links now point to a pattern skeleton or a Connector example, or are removed.
