# Change-log fragments

A pull request never edits a version file or the head of a change log. It adds
one fragment file here instead. A release-prep pull request assembles the
fragments into the release entry and deletes them. Each fragment has a unique
name, so concurrent pull requests do not conflict.

## Targets

| Directory | Release files |
| --- | --- |
| `skills/` | `skills/CHANGELOG.md`, `skills/VERSION`, `skills/dex-sdk/VERSION`, and the plugin and marketplace manifests |
| `go/`, `java/`, `python/`, `rust/`, `typescript/`, `web/`, `cli/`, `server/`, `agents/` | `docs/reference/<target>/changelog.mdx` |
| `agents-packages/` | `agents/CHANGELOG.md` |

A release component names the targets it releases. Each is its target, except
`agents`, which releases both `agents/` and `agents-packages/`.

## Add a fragment

```bash
python3 script/release/changelog_fragments.py new skills --bump patch
```

The command prints the new path, such as
`changelog.d/skills/join-helper-3fa9c1.md`. Write the change-log bullets
below its front matter:

```markdown
---
bump: patch
---
- Correct the ID reuse table in `dex-sdk` `flow.md`: ...
```

- `bump` is `patch`, `minor`, or `major`. See `skills/CONTRIBUTING.md`.
- The body is one or more `- ` bullets. Indent continuation lines. Do not add
  headings; the release adds them.
- A name is `<slug>-<6 lowercase hex digits>.md`.

A component change takes a section instead of a bump:

```bash
python3 script/release/changelog_fragments.py new go --section Fixed
```

```markdown
---
section: Fixed
---
- The **Join** documentation no longer ... [API reference](pathname:///api/go/latest/dex/#type-join).
```

- `section` is `Breaking`, `Added`, `Changed`, `Deprecated`, or `Fixed`.
  `agents-packages` may also name a topic, such as `Dex MCP conventions`.
- Fragments for the docs change logs follow the docs prose rules: **bold**
  for identifiers, no inline backticks, and no braces or `<`.
- A change that ships in several components adds one fragment per target.

Check a fragment with `python3 script/release/changelog_fragments.py lint`.

## Release

The release cutter prepares each release in its own pull request from a fresh
`origin/main` branch:

```bash
python3 script/release/changelog_fragments.py prepare skills --date <YYYY-MM-DD>
```

The **Prepare skills release** workflow does the same and pushes a new
`release-prep/skills-v<version>-<run ID>-<attempt>` branch on every run.

Open the pull request with the `release-prep` label. Check Gate CI reads the
version and date from the new entry, runs the same assembly on the base, and
requires identical files. When a fragment lands on main first, the check fails
and prints the command to prepare again.

Components take the release version and may share one pull request:

```bash
python3 script/release/changelog_fragments.py prepare go,java,python,rust,typescript,web,cli --version 1.45.0 --date <YYYY-MM-DD>
```

- A new minor, or each server-zd `0.1.N`, gets a new entry with its Released
  line. Each agents package release gets its own `agents/CHANGELOG.md` entry.
- A patch folds into its minor's entry: the Released line lists it, and its
  items end with "Since 1.NN.P".
- Preparing a version again, after a fragment landed late, folds the new
  fragments into the existing entry.
- `--allow-empty` releases a component with no fragments as
  "No user-visible changes."
- A newest entry from before fragments, marked "Not released yet" or
  "Unreleased", becomes the release entry, with its items kept.
- Older entries never change.

Merge the release-prep pull request before the tag.
`python3 script/release/changelog_fragments.py check-ready <component> <version>`
fails, and prints the prepare command, while the change log does not record
the release. **Release changed components** runs it for every selected
component.

## Check Gate rules

- Outside a release-prep pull request, any change to a release file fails
  unless the description contains a line `changelog: correction: <reason>`.
- Changing or deleting a fragment that is already on main needs the same line.
- Any change under `skills/`, other than the release files and
  `skills/CHANGELOG-0.x.md`, needs a skills fragment, or the description line
  `skills-release: no bump needed: <reason>`. The mirror publishes that whole
  tree.
- Pull requests opened before fragments existed, up to
  `LEGACY_CHANGELOG_PR_MAX` in `script/release/changelog_fragments.py`, may
  still bump skills the old way. Such a bump must raise `skills/VERSION` above
  the base and add the matching new `skills/CHANGELOG.md` entry. In a component change log they may edit only the
  newest entry that is still unreleased, or open one above the released
  entries, and only while that target has no fragments.
- Reference snapshot pull requests carry the line
  `changelog: correction: reference snapshot`.
