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

## Keep skills and docs in step

`doc-map.yaml` maps each skills file to the docs pages that hold the same
stable knowledge. Each `mappings` entry names one skills file, relative to
this directory, and the English docs pages under `docs/content/primitives/` or
`docs/content/design-patterns/` that it mirrors. The `unmapped` list
classifies files that have no counterpart. Every Markdown file in the three
skills and every page in those two docs folders must be mapped or unmapped, so
a new file needs an explicit decision. An empty map fails.
Check Gate CI fails a pull request that changes a mapped file on one side but
none of its counterparts. When the other side really needs no change, add this
line to the pull request description. Editing the description re-runs Check
Gate CI, which fetches the current description each time it runs:

```text
doc-map: no change needed: <reason>
```

Keep the map current when you add, move, or delete a mapped file. The check
also pairs changes through the base branch's mappings, so removing an entry
does not skip its check in the same pull request.

## Links and boundaries

`script/check-package.py` enforces the content rules that a reviewer would
otherwise miss:

- Local links and heading anchors resolve inside the three skill
  directories, and every file is reachable from its `SKILL.md`.
- External links go only to the docs Developer reference
  (`https://docs.superdurable.io/reference/...`, latest version only) and to
  `examples/` on the `main` branch of `superdurable/dex`. No link pins a
  release tag or commit.
- A few narrow exceptions cover destinations the Developer reference cannot
  replace. Each one is listed with its reason in
  `ALLOWED_EXTERNAL_URL_EXCEPTIONS` in `script/check-package.py`: the
  Connector catalog file, the Connector library root, its examples and
  READMEs on `main`, the `superdurable/dex-skills` repository named in the
  update notice, and its latest-release API used by the version check. Add an
  exception only when no Developer reference page or example can serve.
- Skill content names no product built on Dex, no Continue-As-New, and no
  Temporal or Cadence backend term or mechanism, such as Signals, Queries, and
  Updates or workflow history.
- Skill content has no Dex version numbers, release tags, or dependency pins,
  including Developer reference notes such as "Since 1.35".
  Standards and toolchain versions, such as FDG 2.0 or Node 22, are allowed.
  The two App Builder n8n references may cite n8n releases and node versions.

It does not check exact file lists or sentences.

## Public product boundary

Dex Skills is public Dex material and contains no information about any
product built on Dex (no product names, template contracts or platform
concepts). Guidance covers only the Dex SDKs, `dexcli` including `dexcli dev`,
Dex Server, Dex Web, the FDG analyzer, and the official connectors. Keep
platform-specific guidance for a product built on Dex in that product's own
repository.

## Validate a change

Run from this directory:

```bash
python3 script/check-package.py
python3 script/check-doc-map.py
python3 -m unittest discover -s test -p 'test_*.py'
node --test test/version-check.test.mjs
python3 /path/to/skill-creator/scripts/quick_validate.py dex-sdk
python3 /path/to/skill-creator/scripts/quick_validate.py dex-app-builder
python3 /path/to/skill-creator/scripts/quick_validate.py dex-connector-contributor
```

`check-doc-map.py` needs the Dex checkout because it reads `docs/content/`.
Without arguments it validates the map. Skills CI runs only the checks that
depend on the commit. Check Gate CI runs both scripts with `--base-ref` and
`--pr-body-file`, using the pull request description it fetches from GitHub
when it runs, to enforce the release metadata and the doc-map pairing. A stale
Skills CI run therefore cannot approve a description. A manual dispatch of
Check Gate CI runs only the non-PR checks and validates no pull request
description. To run the description checks locally, pass a base such as
`--base-ref origin/main` and a file with the description.

Also validate the Codex, Claude Code, and Cursor manifests with their current
client tooling and smoke-test that one plugin install exposes exactly the three
expected skills. Confirm App Builder and Connector Contributor resolve the
sibling `dex-sdk` Go handbook without copied references, and that Connector
Contributor routes implementation and acceptance to the target repository's
current instructions.

## Version and release

Bump `VERSION` and add a `CHANGELOG.md` entry only when stable content
changes: a rule, a Primitive concept or relation, a Component, a design
pattern, skill routing, or packaging. Keep every plugin and marketplace
manifest on that exact version where the format supports a version, and keep
`CHANGELOG.md` short. The 0.x release notes are archived in
`CHANGELOG-0.x.md`.

- Increment PATCH for corrections to an existing rule or concept.
- Increment MINOR for new product workflows, languages, patterns, or other
  backward-compatible capabilities.
- Increment MAJOR for incompatible plugin or skill identity changes.

A typo, link, or wording-only change needs no bump. Check Gate CI requires a
`VERSION` and `CHANGELOG.md` update for any change under `dex-sdk/`,
`dex-app-builder/`, or `dex-connector-contributor/`, unless the pull request
description contains this line:

```text
skills-release: no bump needed: <reason>
```

After a pull request lands on Dex `main`, the skills mirror syncs the package.
The mirror's Release workflow validates it and creates `v<version>` from the
matching changelog section. If the tag already exists, the workflow skips
release creation.
