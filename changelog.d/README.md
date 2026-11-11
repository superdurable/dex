# Change-log fragments

A pull request never edits a version file or the head of a change log. It adds
one fragment file here instead. A release-prep pull request assembles the
fragments into the release entry and deletes them. Each fragment has a unique
name, so concurrent pull requests do not conflict.

## Targets

| Directory | Release files |
| --- | --- |
| `skills/` | `skills/CHANGELOG.md`, `skills/VERSION`, `skills/dex-sdk/VERSION`, and the plugin and marketplace manifests |

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
  the base and add the matching new `skills/CHANGELOG.md` entry.
