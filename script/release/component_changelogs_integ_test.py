# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("changelog_fragments.py")
DEX_ROOT = SCRIPT.parents[2]
GO_CHANGELOG = "docs/reference/go/changelog.mdx"
SERVER_CHANGELOG = "docs/reference/server/changelog.mdx"
AGENTS_CHANGELOG = "docs/reference/agents/changelog.mdx"
AGENTS_PACKAGES_CHANGELOG = "agents/CHANGELOG.md"
DOCS_INTRO = "---\nid: changelog\ntitle: Change log\n---\n\n# Change log\n\nUser-visible changes, newest first.\n\n"
GO_RELEASED_1_44 = (
    "## 1.44 {#v1-44}\n\n"
    "Released 2026-11-10 as [sdk-go/v1.44.0](https://github.com/superdurable/dex/releases/tag/sdk-go%2Fv1.44.0).\n\n"
    "### Added\n\n- **Join** synchronizes branches.\n"
)
GO_LEGACY_1_45 = (
    "## 1.45 {#v1-45}\n\n"
    "Not released yet. The next release, sdk-go/v1.45.0, ships these changes.\n\n"
    "### Fixed\n\n- A legacy fix.\n\n"
)
SERVER_RELEASED_0_1_25 = (
    "## 0.1.25 {#v0-1-25}\n\n"
    "Released 2026-11-09 as [server-zd/v0.1.25](https://github.com/superdurable/dex/releases/tag/server-zd%2Fv0.1.25).\n\n"
    "### Fixed\n\n- A server fix.\n"
)
AGENTS_DOCS = (
    DOCS_INTRO
    + "## Unreleased {#unreleased}\n\nNot released yet. The next agents release ships these changes.\n\n"
    "### Added\n\n- **McpToolSource** follows the conventions.\n\n"
    "## 0.4 {#v0-4}\n\n"
    "Released 2026-11-10 as [agents/v0.4.0](https://github.com/superdurable/dex/releases/tag/agents%2Fv0.4.0).\n\n"
    "### Added\n\n- Connector tools.\n"
)
AGENTS_PACKAGES = (
    "# Changelog\n\nAll agents packages share this changelog.\n\n"
    "## Unreleased\n\n### Added\n\n- `McpToolSource` follows the conventions.\n\n### Images\n\n- A media store.\n\n"
    "## 0.4.0 (2026-11-10)\n\n### Connector tools\n\n- Tools.\n"
)
LEGACY_PR_NUMBER = "1500"
NEW_PR_NUMBER = "9000"


def next_release(changelog: str) -> str:
    """Returns the version the newest entry of a real change log releases next: its own key, or the next minor."""
    heading = re.search(r"^## (\d+)\.(\d+)(?:\.(\d+))? \{#v[0-9-]+\}\n\n(?P<state>Not released|Released)", changelog, re.M)
    unreleased = re.search(r"^## Unreleased", changelog, re.M)
    if unreleased and (heading is None or unreleased.start() < heading.start()):
        major, minor, _ = (int(part) for part in re.search(r"agents%2Fv(\d+)\.(\d+)\.(\d+)", changelog).groups())
        return f"{major}.{minor + 1}.0"
    major, minor, patch = (int(part) if part else 0 for part in heading.group(1, 2, 3))
    if heading.group("state") == "Not released":
        return f"{major}.{minor}.{patch}"
    return f"{major}.{minor}.{patch + 1}" if heading.group(3) else f"{major}.{minor + 1}.0"


class ComponentChangelogsIntegrationTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.repository = Path(self.temporary_directory.name) / "dex"
        self.repository.mkdir()
        self.git("init", "-b", "main")
        self.git("config", "user.name", "Release Test")
        self.git("config", "user.email", "release-test@example.com")
        self.write(GO_CHANGELOG, DOCS_INTRO + GO_LEGACY_1_45 + GO_RELEASED_1_44)
        self.write("docs/reference/java/changelog.mdx", DOCS_INTRO + GO_RELEASED_1_44.replace("sdk-go", "sdk-java"))
        self.write(SERVER_CHANGELOG, DOCS_INTRO + SERVER_RELEASED_0_1_25)
        self.write(AGENTS_CHANGELOG, AGENTS_DOCS)
        self.write(AGENTS_PACKAGES_CHANGELOG, AGENTS_PACKAGES)
        self.write("changelog.d/go/.gitkeep", "")
        self.commit("initial")
        self.body_file = Path(self.temporary_directory.name) / "body.txt"
        self.set_body("## Summary\n")

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def test_new_writes_a_section_skeleton(self) -> None:
        path = self.run_script("new", "go", "--section", "Fixed", "--slug", "join-fix").stdout.strip()
        self.assertRegex(path, r"^changelog\.d/go/join-fix-[0-9a-f]{6}\.md$")
        self.assertEqual(self.read(path), "---\nsection: Fixed\n---\n- \n")
        self.assertIn("takes --section", self.run_script("new", "go", "--bump", "patch", check=False).stderr)

    def test_lint_applies_the_docs_prose_rules_to_docs_targets_only(self) -> None:
        cases = {
            "changelog.d/go/backticks-aaaaaa.md": ("---\nsection: Fixed\n---\n- Fix `Join`.\n", "not inline backticks"),
            "changelog.d/go/braces-aaaaaa.md": ("---\nsection: Fixed\n---\n- Fix {x}.\n", "braces"),
            "changelog.d/go/section-aaaaaa.md": ("---\nsection: Removed\n---\n- Fix.\n", "section must be one of"),
            "changelog.d/go/bump-aaaaaa.md": ("---\nbump: patch\n---\n- Fix.\n", "exactly the front matter key 'section'"),
        }
        for path, (text, message) in cases.items():
            with self.subTest(path=path):
                self.write(path, text)
                result = self.run_script("lint", path, check=False)
                self.assertEqual(result.returncode, 1)
                self.assertIn(message, result.stderr)
        topic = "changelog.d/agents-packages/images-aaaaaa.md"
        self.write(topic, "---\nsection: Images\n---\n- `MediaStore` keeps images.\n")
        self.assertEqual(self.run_script("lint", topic).returncode, 0)

    def test_prepare_absorbs_the_legacy_entry_and_keeps_older_entries(self) -> None:
        self.write("changelog.d/go/b-fix-000002.md", "---\nsection: Fixed\n---\n- A fragment fix.\n")
        self.write("changelog.d/go/a-api-000001.md", "---\nsection: Added\n---\n- **Join.Reset** resets a round.\n")

        self.run_script("prepare", "go", "--version", "1.45.0", "--date", "2026-11-12")

        self.assertEqual(
            self.read(GO_CHANGELOG),
            DOCS_INTRO
            + "## 1.45 {#v1-45}\n\n"
            "Released 2026-11-12 as [sdk-go/v1.45.0](https://github.com/superdurable/dex/releases/tag/sdk-go%2Fv1.45.0).\n\n"
            "### Added\n\n- **Join.Reset** resets a round.\n\n"
            "### Fixed\n\n- A legacy fix.\n- A fragment fix.\n\n"
            + GO_RELEASED_1_44,
        )
        self.assertEqual(sorted(path.name for path in (self.repository / "changelog.d/go").iterdir()), [".gitkeep"])

    def test_prepare_folds_a_patch_and_a_repeated_prepare_into_the_minor(self) -> None:
        self.run_script("prepare", "go", "--version", "1.45.0", "--date", "2026-11-12")
        self.commit("1.45.0")
        self.write("changelog.d/go/patch-000001.md", "---\nsection: Fixed\n---\n- A patch fix.\n")
        self.run_script("prepare", "go", "--version", "1.45.1", "--date", "2026-11-13")
        self.commit("1.45.1")
        self.write("changelog.d/go/late-000002.md", "---\nsection: Changed\n---\n- A late change.\n")
        self.run_script("prepare", "go", "--version", "1.45.1", "--date", "2026-11-13")

        changelog = self.read(GO_CHANGELOG)
        self.assertIn(
            "Released 2026-11-12 as [sdk-go/v1.45.0](https://github.com/superdurable/dex/releases/tag/sdk-go%2Fv1.45.0); "
            "1.45.1 released 2026-11-13 as [sdk-go/v1.45.1](https://github.com/superdurable/dex/releases/tag/sdk-go%2Fv1.45.1).\n",
            changelog,
        )
        self.assertEqual(changelog.count("1.45.1 released"), 1)
        self.assertIn("### Changed\n\n- A late change. Since 1.45.1.\n\n### Fixed\n\n- A legacy fix.\n- A patch fix. Since 1.45.1.\n", changelog)
        self.assertEqual(changelog.count("## 1.45 "), 1)
        self.assertTrue(changelog.endswith(GO_RELEASED_1_44))

    def test_prepare_writes_a_new_entry_and_needs_allow_empty_without_changes(self) -> None:
        java = "docs/reference/java/changelog.mdx"
        before = self.read(java)
        result = self.run_script("prepare", "java", "--version", "1.45.0", "--date", "2026-11-12", check=False)
        self.assertEqual(result.returncode, 1)
        self.assertIn("--allow-empty", result.stderr)

        self.run_script("prepare", "java", "--version", "1.45.0", "--date", "2026-11-12", "--allow-empty")

        self.assertEqual(
            self.read(java),
            DOCS_INTRO
            + "## 1.45 {#v1-45}\n\n"
            "Released 2026-11-12 as [sdk-java/v1.45.0](https://github.com/superdurable/dex/releases/tag/sdk-java%2Fv1.45.0).\n\n"
            "No user-visible changes.\n\n"
            + before[len(DOCS_INTRO) :],
        )

    def test_prepare_rejects_an_older_version_and_a_mismatched_legacy_entry(self) -> None:
        result = self.run_script("prepare", "go", "--version", "1.44.1", "--date", "2026-11-12", check=False)
        self.assertEqual(result.returncode, 1)
        self.assertIn("the unreleased entry 1.45 does not match release 1.44.1", result.stderr)
        self.write("changelog.d/java/fix-000001.md", "---\nsection: Fixed\n---\n- Fix.\n")
        result = self.run_script("prepare", "java", "--version", "1.43.0", "--date", "2026-11-12", check=False)
        self.assertIn("release 1.43.0 is older than the newest entry 1.44", result.stderr)

    def test_server_gets_an_entry_per_release(self) -> None:
        self.write("changelog.d/server/fix-000001.md", "---\nsection: Fixed\n---\n- A new fix.\n")
        self.run_script("prepare", "server", "--version", "0.1.26", "--date", "2026-11-12")
        self.assertEqual(
            self.read(SERVER_CHANGELOG),
            DOCS_INTRO
            + "## 0.1.26 {#v0-1-26}\n\n"
            "Released 2026-11-12 as [server-zd/v0.1.26](https://github.com/superdurable/dex/releases/tag/server-zd%2Fv0.1.26).\n\n"
            "### Fixed\n\n- A new fix.\n\n"
            + SERVER_RELEASED_0_1_25,
        )

    def test_agents_prepares_both_change_logs(self) -> None:
        self.write("changelog.d/agents/fix-000001.md", "---\nsection: Fixed\n---\n- **Session** fix.\n")
        self.write("changelog.d/agents-packages/mcp-000001.md", "---\nsection: Dex MCP conventions\n---\n- `_meta` keys.\n")
        self.write("changelog.d/agents-packages/break-000002.md", "---\nsection: Breaking\n---\n- Renamed `x`.\n")

        self.run_script("prepare", "agents", "--version", "0.5.0", "--date", "2026-11-12")

        docs = self.read(AGENTS_CHANGELOG)
        self.assertIn(
            "## 0.5 {#v0-5}\n\n"
            "Released 2026-11-12 as [agents/v0.5.0](https://github.com/superdurable/dex/releases/tag/agents%2Fv0.5.0).\n\n"
            "### Added\n\n- **McpToolSource** follows the conventions.\n\n### Fixed\n\n- **Session** fix.\n\n## 0.4 {#v0-4}",
            docs,
        )
        self.assertNotIn("Unreleased", docs)
        self.assertEqual(
            self.read(AGENTS_PACKAGES_CHANGELOG),
            "# Changelog\n\nAll agents packages share this changelog.\n\n"
            "## 0.5.0 (2026-11-12)\n\n### Breaking\n\n- Renamed `x`.\n\n### Dex MCP conventions\n\n- `_meta` keys.\n\n"
            "### Added\n\n- `McpToolSource` follows the conventions.\n\n### Images\n\n- A media store.\n\n"
            "## 0.4.0 (2026-11-10)\n\n### Connector tools\n\n- Tools.\n",
        )

    def test_agents_packages_absorb_a_versioned_unreleased_entry(self) -> None:
        self.write(AGENTS_PACKAGES_CHANGELOG, AGENTS_PACKAGES.replace("## Unreleased\n", "## 0.5.0 (not released yet)\n"))
        result = self.run_script("prepare", "agents", "--version", "0.6.0", "--date", "2026-11-12", check=False)
        self.assertIn("the unreleased entry 0.5.0 does not match release 0.6.0", result.stderr)

        self.run_script("prepare", "agents", "--version", "0.5.0", "--date", "2026-11-12")

        packages = self.read(AGENTS_PACKAGES_CHANGELOG)
        self.assertIn("## 0.5.0 (2026-11-12)\n\n### Added\n\n- `McpToolSource` follows the conventions.\n", packages)
        self.assertNotIn("not released yet", packages)
        self.assertEqual(self.run_script("check-ready", "agents", "0.5.0").returncode, 0)

    def test_check_ready_needs_a_merged_prepare(self) -> None:
        result = self.run_script("check-ready", "go", "1.45.0", check=False)
        self.assertEqual(result.returncode, 1)
        self.assertIn("the newest entry is still unreleased", result.stderr)
        self.assertIn("prepare go --version 1.45.0 --date <YYYY-MM-DD>", result.stderr)

        self.run_script("prepare", "go", "--version", "1.45.0", "--date", "2026-11-12")
        self.assertEqual(self.run_script("check-ready", "go", "1.45.0").returncode, 0)
        self.write("changelog.d/go/late-000001.md", "---\nsection: Fixed\n---\n- Late.\n")
        self.assertIn("1 fragments are not in", self.run_script("check-ready", "go", "1.45.0", check=False).stderr)
        self.assertIn(
            f"{AGENTS_PACKAGES_CHANGELOG}: the newest entry is still unreleased",
            self.run_script("check-ready", "agents", "0.5.0", check=False).stderr,
        )

    def test_verify_accepts_a_multi_component_prep_including_absorption_and_empty_releases(self) -> None:
        self.land("changelog.d/server/fix-000001.md", "---\nsection: Fixed\n---\n- A new fix.\n")
        self.git("switch", "-c", "release-prep/1.45.0")
        self.run_script("prepare", "go,java", "--version", "1.45.0", "--date", "2026-11-12", "--allow-empty")
        self.run_script("prepare", "server", "--version", "0.1.26", "--date", "2026-11-12")
        self.commit("prepare")

        result = self.check_pr(labels="release-prep")
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_verify_rejects_a_tampered_entry_and_a_base_that_gained_a_fragment(self) -> None:
        self.git("switch", "-c", "release-prep/1.45.0")
        self.run_script("prepare", "go", "--version", "1.45.0", "--date", "2026-11-12")
        self.write(GO_CHANGELOG, self.read(GO_CHANGELOG).replace("A legacy fix.", "A reworded fix."))
        self.commit("tampered prepare")
        result = self.check_pr(labels="release-prep")
        self.assertEqual(result.returncode, 1)
        self.assertIn(f"{GO_CHANGELOG} differs from the prepare output", result.stderr)

        self.git("reset", "-q", "--hard", "HEAD~1")
        self.run_script("prepare", "go", "--version", "1.45.0", "--date", "2026-11-12")
        self.commit("prepare")
        self.git("switch", "main")
        self.land("changelog.d/go/late-000001.md", "---\nsection: Fixed\n---\n- Late.\n")
        self.git("switch", "-c", "merge-ref")
        self.git("merge", "--no-ff", "--no-edit", "release-prep/1.45.0")
        result = self.check_pr(base_ref="HEAD^1", labels="release-prep")
        self.assertEqual(result.returncode, 1)
        self.assertIn("go fragments this release does not consume: changelog.d/go/late-000001.md", result.stderr)
        self.assertIn("prepare go --version 1.45.0 --date 2026-11-12", result.stderr)

    def test_component_change_log_edits_need_release_prep_or_a_correction_line(self) -> None:
        self.git("switch", "-c", "edit")
        self.write(GO_CHANGELOG, self.read(GO_CHANGELOG).replace("Old", "Older").replace("synchronizes", "joins"))
        self.commit("edit")
        result = self.check_pr()
        self.assertEqual(result.returncode, 1)
        self.assertIn(f"only a release-prep pull request changes {GO_CHANGELOG}", result.stderr)
        self.assertIn("new go --section <section>", result.stderr)
        self.set_body("changelog: correction: reference snapshot\n")
        self.assertEqual(self.check_pr().returncode, 0)

    def test_grace_allows_only_edits_of_the_unreleased_entry(self) -> None:
        self.git("switch", "-c", "legacy-edit")
        self.write(GO_CHANGELOG, self.read(GO_CHANGELOG).replace("- A legacy fix.\n", "- A legacy fix.\n- Another fix.\n"))
        self.commit("legacy edit")
        self.assertEqual(self.check_pr(pr_number=LEGACY_PR_NUMBER).returncode, 0)
        self.assertEqual(self.check_pr().returncode, 1)
        self.write("changelog.d/go/pending-000001.md", "---\nsection: Fixed\n---\n- Pending.\n")
        self.commit("pending fragment")
        self.assertIn("only while the target has no fragments", self.check_pr(pr_number=LEGACY_PR_NUMBER).stderr)
        self.git("rm", "-q", "changelog.d/go/pending-000001.md")
        self.commit("drop pending fragment")

        self.write(GO_CHANGELOG, self.read(GO_CHANGELOG).replace("- **Join** synchronizes", "- New.\n- **Join** synchronizes"))
        self.commit("released edit")
        result = self.check_pr(pr_number=LEGACY_PR_NUMBER)
        self.assertEqual(result.returncode, 1)
        self.assertIn("may edit only the newest unreleased entry", result.stderr)

    def test_grace_may_open_an_unreleased_entry_but_not_reopen_a_released_one(self) -> None:
        java = "docs/reference/java/changelog.mdx"
        released = self.read(java)
        self.git("switch", "-c", "open-entry")
        legacy = GO_LEGACY_1_45.replace("sdk-go", "sdk-java")
        self.write(java, DOCS_INTRO + legacy + released[len(DOCS_INTRO) :])
        self.commit("open 1.45")
        self.assertEqual(self.check_pr(pr_number=LEGACY_PR_NUMBER).returncode, 0)

        reopened = legacy.replace("## 1.45 {#v1-45}", "## 1.44 {#v1-44}")
        self.write(java, DOCS_INTRO + reopened + released[len(DOCS_INTRO) :])
        self.commit("reopen 1.44")
        self.assertEqual(self.check_pr(pr_number=LEGACY_PR_NUMBER).returncode, 1)

    def test_prepare_accepts_every_current_change_log(self) -> None:
        for component in ("go", "java", "python", "rust", "typescript", "web", "cli", "server", "agents"):
            with self.subTest(component=component):
                output = AGENTS_CHANGELOG if component == "agents" else f"docs/reference/{component}/changelog.mdx"
                outputs = [output, AGENTS_PACKAGES_CHANGELOG] if component == "agents" else [output]
                for path in outputs:
                    self.write(path, (DEX_ROOT / path).read_text(encoding="utf-8"))
                version = next_release(self.read(output))
                before = self.read(output)
                self.run_script("prepare", component, "--version", version, "--date", "2026-11-12", "--allow-empty")
                self.assertEqual(self.run_script("check-ready", component, version).returncode, 0)
                released = before.index("\nReleased ")
                older = before[before.rindex("\n## ", 0, released) + 1 :]
                self.assertTrue(self.read(output).endswith(older), output)

    def land(self, path: str, text: str) -> None:
        self.write(path, text)
        self.commit(f"land {path}")

    def check_pr(
        self, base_ref: str = "main", labels: str = "", pr_number: str = NEW_PR_NUMBER
    ) -> subprocess.CompletedProcess[str]:
        return self.run_script(
            "check-pr", "--base-ref", base_ref, "--pr-body-file", str(self.body_file), "--pr-number", pr_number,
            "--labels", labels, check=False,
        )

    def run_script(self, *arguments: str, check: bool = True) -> subprocess.CompletedProcess[str]:
        result = subprocess.run(
            (sys.executable, str(SCRIPT), *arguments),
            cwd=self.repository, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        if check:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def set_body(self, body: str) -> None:
        self.body_file.write_text(body, encoding="utf-8")

    def read(self, relative_path: str) -> str:
        return (self.repository / relative_path).read_text(encoding="utf-8")

    def write(self, relative_path: str, content: str) -> None:
        path = self.repository / relative_path
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")

    def commit(self, message: str) -> None:
        self.git("add", "-A")
        self.git("commit", "-q", "--allow-empty", "-m", message)

    def git(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ("git", *arguments), cwd=self.repository, check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE
        )


if __name__ == "__main__":
    unittest.main()
