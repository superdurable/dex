# Changelog

Release notes for Dex Skills 1.0.0 and later. The notes for 0.1.0 through
0.69.1 are archived in [CHANGELOG-0.x.md](CHANGELOG-0.x.md).

## 1.0.0 - 2026-11-06

- Rewrite the Skills as the coding-agent version of the Dex stable layer. They cover the same scope as the docs Primitives, Components, and Design patterns: mental models, decision tables, rules and anti-patterns, design patterns with code skeletons, language idioms, and a self-check list. Each rule is complete without its links.
- Remove version numbers, release history, volatile parameters, and pinned source excerpts. Defaults, limits, configuration, flags, and version differences now live in SDK doc comments and the versioned docs Developer reference.
- Link only to the Developer reference and to runnable examples on the default branch. `dex-sdk` tells the agent to find the project's SDK version and open the matching version of the Developer reference. No link pins a release tag or commit.
- Replace the validators. `check-package.py` now checks packaging, front matter, the three-skill layout, local links and anchors in the standalone bundle, an external URL allow-list, the product and concept boundaries, and that skill content has no Dex version numbers. It no longer checks exact files or sentences. The source-excerpt and upstream-baseline validators and all `DEX_*BASELINE` files are removed, so a Dex release never requires a skills change.
- Add `doc-map.yaml`, which maps each skills file to the docs pages that hold the same stable knowledge. Skills CI fails a pull request that changes one side of the map without the other, unless its description states why with `doc-map: no change needed: <reason>`.
- Release only when stable content changes. A typo, link, or wording-only change states `skills-release: no bump needed: <reason>` in its pull request instead of bumping the version. The 0.x release notes moved to `CHANGELOG-0.x.md`.
