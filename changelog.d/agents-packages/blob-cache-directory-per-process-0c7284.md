---
section: Changed
---
- The TypeScript, Python, and Go READMEs now say beside each `.dex-cache` example that only one running process may use a blob cache directory. Give each Worker or Client process its own directory (#2185).
- The `agentWorkerBlobCacheDirectoryVariable` docs in `@superdurable/dex-agents/testing` now say to give each Worker child that runs at the same time its own directory (#2185).
