---
section: Added
---
- **dexcli visualize** accepts **--typescript-compiler**, or the
  **DEX_TYPESCRIPT_COMPILER** environment variable, naming a trusted
  **typescript** package directory. The analyzer then loads only that
  compiler and starts Node.js without **NODE_** environment variables, so no
  code from the analyzed source tree runs. Without it, the analyzer loads the
  **typescript** package that the source project resolves, which a source tree
  can supply through its own or a parent **node_modules** or a package named
  **typescript**, even when **NODE_PATH** is set. Set it whenever you
  visualize untrusted TypeScript source, and keep the analyzed tree's
  directories out of **PATH**. An empty flag value or an empty variable is a
  usage error, never a fallback to the source project's compiler.
  [Reference](/reference/web/flow-visualization#typescript-compiler).
