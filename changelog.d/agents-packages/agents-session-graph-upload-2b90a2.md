---
section: Dex SDK 1.45.0
---
- Every package requires Dex SDK 1.45.0 (`dex-python-sdk==1.45.0`,
  `@superdurable/dex` `1.45.0`, Go `sdk-go` `v1.45.0`) instead of 1.44.0, for
  Worker-side Flow definition upload. Before upgrading, read the breaking
  change of SDK 1.45 in the [Python](../docs/reference/python/changelog.mdx),
  [TypeScript](../docs/reference/typescript/changelog.mdx), and
  [Go](../docs/reference/go/changelog.mdx) SDK change logs: a Step type with
  "-" can no longer start SubFlows. The agents packages start none.
- Agents CI, the nightly long run, and the publish workflows test against
  `dexcli` 1.45.0. The Python and TypeScript CLI cores download `dexcli`
  1.45.0 when none is installed.
