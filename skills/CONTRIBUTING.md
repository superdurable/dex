# Contributing

The source of truth is [superdurable/dex `skills/`](https://github.com/superdurable/dex/tree/main/skills).
Contribute there; the `superdurable/dex-skills` mirror accepts no pull requests.
Dex runs the validators in [Skills CI](https://github.com/superdurable/dex/blob/main/.github/workflows/skills-ci.yml).

This repository publishes one plugin with three root-level skills:

- `dex-sdk` owns public Dex SDK implementation guidance and its Core and
  language references.
- `dex-app-builder` owns the business-first product workflow and its stricter
  Go backend constraints. It links to `dex-sdk` instead of copying SDK
  references.
- `dex-connector-contributor` is the thin bootstrap for official
  connector-library work. It resolves the checkout and then follows that
  repository's rules, while linking to `dex-sdk` Core/Go when needed.

Keep every retained reference reachable from its skill entrypoint. Connector
Contributor intentionally has no supporting references because the target
repository owns that guidance. Do not add a `plugins/` wrapper, a backend
companion skill, or duplicated Core/Go handbooks.

## Skill content

The Skills are the coding-agent version of Dex's stable docs layer. They cover
the same scope as the docs Primitives, Components, and Design pattern pages,
written as mental models, decision tables, rules, anti-patterns, code
skeletons, and checklists. Each rule must be complete without its links.

Ask one question of each fact: will this still hold in the next minor release?
If yes, it may belong in the Skills. If no, it belongs in SDK doc comments or
the versioned docs Developer reference. Skills contain no Dex version numbers
and no release-tag pins. They link only to the Developer reference and to
`examples/`, never to the Primitives, Components, or Design pattern pages.

Change the Skills only when a stable programming rule, a Primitive concept or
relation, a Component, or a design pattern changes. Update the matching docs
pages in the same pull request, or state in the pull request why they need no
change. A skills change does not wait for a Dex release, and a Dex release
never requires a skills change.

## Transitional: pinned baselines

Phase P6 of the docs and skills cleanup replaces the validators below and
removes the baseline pins. Until then, the existing validators must keep
passing, so avoid editing pinned skills content. Do not advance a baseline
only because Dex released. When you must touch pinned content, these rules
still apply.

`DEX_BASELINE` pins exact Go SDK excerpts. `DEX_JAVA_BASELINE`,
`DEX_PYTHON_BASELINE`, `DEX_TYPESCRIPT_BASELINE`, and `DEX_RUST_BASELINE`
pin each other SDK and its language examples to its own published tag. Language code fences must be contiguous
excerpts from runnable examples, SDK tests, or SDK READMEs at that release, with
the existing visible source link and `dex-source` marker.

`DEX_SERVER_BASELINE` pins the published server-zd release that the CLI
baseline bundles (`cli/SERVER_ZD_RELEASE`), the only Dex Server. The
Temporal-based Go Server was removed after `server/v1.25.0`; no baseline or
source link may name it. `DEX_CLI_BASELINE` pins the released local tooling and
embedded Web v2/FDG 2.0 implementation. `DEX_WEB_BASELINE` pins the standalone
Dex Web release (`web/vX.Y.Z`), and `web/` and `protos/` source links use it.
Refresh any baseline deliberately and review all affected guidance. The CLI
checkout must include all published tags (use a full fetch, as Skills CI does);
source validation reads the non-Go SDK tags and the Web tag there even when the
Go checkout is shallow.

## Public product boundary

Dex Skills is public Dex material and contains no information about any
product built on Dex (no product names, template contracts or platform
concepts). Guidance covers only the Dex SDKs, `dexcli` including `dexcli dev`,
Dex Server, Dex Web, the FDG analyzer, and the official connectors. Keep
platform-specific guidance for a product built on Dex in that product's own
repository.

## Validate a change

Run from the repository root:

```bash
python3 script/check-package.py
python3 script/check-reference-sources.py \
  --dex-root /path/to/dex-sdk-baseline \
  --server-root /path/to/dex-server-zd-baseline \
  --cli-root /path/to/dex-cli-baseline
python3 script/check-upstream-baselines.py \
  --dex-root /path/to/dex-cli-baseline
python3 -m unittest discover -s test -p 'test_*.py'
node --test test/version-check.test.mjs
python3 /path/to/skill-creator/scripts/quick_validate.py dex-sdk
python3 /path/to/skill-creator/scripts/quick_validate.py dex-app-builder
python3 /path/to/skill-creator/scripts/quick_validate.py dex-connector-contributor
```

Also validate the Codex, Claude Code, and Cursor manifests with their current
client tooling and smoke-test that one plugin install exposes exactly the three
expected skills. Confirm App Builder and Connector Contributor resolve the
sibling `dex-sdk` Go handbook without copied references, and that Connector
Contributor routes implementation and acceptance to the target repository's
current instructions.

## Version and release

Bump `VERSION` and add a `CHANGELOG.md` entry only when stable content
changes: a rule, a Primitive concept or relation, a Component, a design
pattern, skill routing, or packaging. Typo fixes, link repairs, and wording
clarifications wait for the next such release. Keep every plugin and
marketplace manifest on that exact version where the format supports a
version.

- Increment PATCH for corrections to an existing rule or concept.
- Increment MINOR for new product workflows, languages, patterns, or other
  backward-compatible capabilities.
- Increment MAJOR for incompatible plugin or skill identity changes.

Transitional: until phase P6, Skills CI still requires every change under
`dex-sdk/`, `dex-app-builder/`, or `dex-connector-contributor/` to update
`VERSION` and `CHANGELOG.md`.

After a pull request lands on Dex `main`, the skills mirror syncs the package.
The mirror's Release workflow validates it and creates `v<version>` from the
matching changelog section. If the tag already exists, the workflow skips
release creation.
