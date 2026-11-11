# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import importlib.util
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("changelog_fragments.py")
DEX_ROOT = SCRIPT.parents[2]
MANIFESTS = (
    "skills/.claude-plugin/plugin.json",
    "skills/.claude-plugin/marketplace.json",
    "skills/.codex-plugin/plugin.json",
    "skills/.cursor-plugin/plugin.json",
    "skills/.cursor-plugin/marketplace.json",
)
SKILL_FILE = "skills/dex-sdk/SKILL.md"
LEGACY_PR_NUMBER = "1500"
NEW_PR_NUMBER = "9000"


class ChangelogFragmentsIntegrationTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.repository = Path(self.temporary_directory.name) / "dex"
        self.repository.mkdir()
        self.git("init", "-b", "main")
        self.git("config", "user.name", "Release Test")
        self.git("config", "user.email", "release-test@example.com")
        shutil.copytree(
            DEX_ROOT / "skills", self.repository / "skills", ignore=shutil.ignore_patterns("__pycache__", ".DS_Store")
        )
        self.write(".gitignore", "__pycache__/\n")
        self.write("changelog.d/skills/.gitkeep", "")
        self.commit("initial")
        self.base_version = self.read("skills/VERSION").strip()
        self.body_file = Path(self.temporary_directory.name) / "body.txt"
        self.set_body("## Summary\n\nA change.\n")

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def test_new_writes_a_unique_name_whose_skeleton_must_be_filled(self) -> None:
        first = self.run_script("new", "skills", "--bump", "minor", "--slug", "Feature/Join Helper!").stdout.strip()
        second = self.run_script("new", "skills", "--bump", "minor", "--slug", "Feature/Join Helper!").stdout.strip()

        self.assertRegex(first, r"^changelog\.d/skills/join-helper-[0-9a-f]{6}\.md$")
        self.assertNotEqual(first, second)
        self.assertIn("empty", self.run_script("lint", first, check=False).stderr)
        self.write(first, "---\nbump: minor\n---\n- Add the Join pattern.\n")
        self.assertEqual(self.run_script("lint", first).returncode, 0)

    def test_new_retries_a_taken_name_and_never_overwrites_it(self) -> None:
        directory = self.repository / "changelog.d/skills"
        taken = directory / "taken-aaaaaa.md"
        taken.write_text("keep\n", encoding="utf-8")
        suffixes = iter(("aaaaaa", "bbbbbb"))
        module = load_script_module()
        module.secrets.token_hex = lambda _: next(suffixes)

        path = module.write_new_fragment(directory, "taken", "patch")

        self.assertEqual(path, directory / "taken-bbbbbb.md")
        self.assertEqual(taken.read_text(encoding="utf-8"), "keep\n")
        module.secrets.token_hex = lambda _: "aaaaaa"
        with self.assertRaisesRegex(module.FragmentError, "no free fragment name"):
            module.write_new_fragment(directory, "taken", "patch")

    def test_new_defaults_to_the_branch_name(self) -> None:
        self.git("switch", "-c", "feature/rename-step-docs")
        path = self.run_script("new", "skills", "--bump", "patch").stdout.strip()
        self.assertRegex(path, r"^changelog\.d/skills/rename-step-docs-[0-9a-f]{6}\.md$")

    def test_lint_rejects_malformed_fragments(self) -> None:
        cases = {
            "changelog.d/skills/no-suffix.md": ("---\nbump: patch\n---\n- Fix.\n", "6 lowercase hex"),
            "changelog.d/skills/bad-bump-aaaaaa.md": ("---\nbump: huge\n---\n- Fix.\n", "bump must be one of"),
            "changelog.d/skills/extra-key-aaaaaa.md": ("---\nbump: patch\nsection: Fixed\n---\n- Fix.\n", "exactly the front matter key"),
            "changelog.d/skills/no-front-matter-aaaaaa.md": ("- Fix.\n", "front matter"),
            "changelog.d/skills/heading-aaaaaa.md": ("---\nbump: patch\n---\n- Fix.\n## 9.9.9\n", "heading"),
            "changelog.d/skills/prose-aaaaaa.md": ("---\nbump: patch\n---\nFix things.\n", "'- ' bullet"),
            "changelog.d/skills/product-aaaaaa.md": ("---\nbump: patch\n---\n- Explain Super Verse.\n", "product built on Dex"),
            "changelog.d/other/target-aaaaaa.md": ("---\nbump: patch\n---\n- Fix.\n", "target one of"),
        }
        for path, (text, message) in cases.items():
            with self.subTest(path=path):
                self.write(path, text)
                result = self.run_script("lint", path, check=False)
                self.assertEqual(result.returncode, 1)
                self.assertIn(message, result.stderr)

    def test_lint_accepts_continuation_lines(self) -> None:
        path = "changelog.d/skills/wrapped-abcdef.md"
        self.write(path, "---\nbump: patch\n---\n- First rule,\n  wrapped.\n- Second rule.\n")
        self.assertEqual(self.run_script("lint").returncode, 0)

    def test_prepare_bumps_by_the_largest_fragment_and_orders_by_path(self) -> None:
        self.write("changelog.d/skills/b-rule-000001.md", "---\nbump: patch\n---\n- Correct the B rule.\n")
        self.write("changelog.d/skills/a-pattern-000002.md", "---\nbump: minor\n---\n- Add the A pattern.\n")
        manifests_before = {path: self.read(path) for path in MANIFESTS}
        changelog_before = self.read("skills/CHANGELOG.md")

        self.run_script("prepare", "skills", "--date", "2026-11-12")

        major, minor, _ = (int(part) for part in self.base_version.split("."))
        version = f"{major}.{minor + 1}.0"
        self.assertEqual(self.read("skills/VERSION"), f"{version}\n")
        self.assertEqual(self.read("skills/dex-sdk/VERSION"), f"{version}\n")
        entry = f"## {version} - 2026-11-12\n\n- Add the A pattern.\n- Correct the B rule.\n\n"
        newest = changelog_before.index("\n## ") + 1
        self.assertEqual(self.read("skills/CHANGELOG.md"), changelog_before[:newest] + entry + changelog_before[newest:])
        for path, before in manifests_before.items():
            expected = before.replace(f'"version": "{self.base_version}"', f'"version": "{version}"')
            self.assertEqual(self.read(path), expected, path)
        self.assertEqual(sorted(path.name for path in (self.repository / "changelog.d/skills").iterdir()), [".gitkeep"])
        self.run_check_package()

    def test_prepare_releases_a_major_and_a_patch(self) -> None:
        major = int(self.base_version.split(".")[0])
        self.write("changelog.d/skills/identity-000001.md", "---\nbump: major\n---\n- Rename the plugin.\n")
        self.write("changelog.d/skills/typo-000002.md", "---\nbump: patch\n---\n- Correct a rule.\n")
        self.run_script("prepare", "skills", "--date", "2026-11-12")
        self.assertEqual(self.read("skills/VERSION"), f"{major + 1}.0.0\n")

        self.commit("major release")
        self.write("changelog.d/skills/fix-000003.md", "---\nbump: patch\n---\n- Correct another rule.\n")
        self.run_script("prepare", "skills", "--date", "2026-11-13")
        self.assertEqual(self.read("skills/VERSION"), f"{major + 1}.0.1\n")
        self.run_check_package()

    def test_prepare_fails_without_fragments_or_with_a_bad_date(self) -> None:
        result = self.run_script("prepare", "skills", "--date", "2026-11-12", check=False)
        self.assertEqual(result.returncode, 1)
        self.assertIn("nothing to release", result.stderr)

        self.write("changelog.d/skills/fix-000001.md", "---\nbump: patch\n---\n- Fix.\n")
        result = self.run_script("prepare", "skills", "--date", "2026-02-30", check=False)
        self.assertEqual(result.returncode, 1)
        self.assertIn("not a calendar date", result.stderr)
        self.assertEqual(self.read("skills/VERSION").strip(), self.base_version)

    def test_verify_accepts_exactly_the_prepare_output(self) -> None:
        self.land_fragments("fix-000001")
        self.prepare_branch("2026-11-12")

        self.assertEqual(self.run_script("verify-release-prep", "--base-ref", "main").returncode, 0)
        result = self.check_pr(labels="release-prep")
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_verify_rejects_a_tampered_entry_a_wrong_version_and_extra_files(self) -> None:
        self.land_fragments("fix-000001")
        self.prepare_branch("2026-11-12")
        prepared = self.git("rev-parse", "HEAD").stdout.strip()
        cases = {
            "tampered": ("skills/CHANGELOG.md", lambda text: text.replace("- Fix fix-000001.", "- Fix it.", 1), "CHANGELOG.md differs"),
            "version": ("skills/VERSION", lambda text: "9.9.9\n", "VERSION differs"),
            "heading": ("skills/CHANGELOG.md", lambda text: re.sub(r"^## \S+", "## 9.9.9", text, count=1, flags=re.M), "the new entry is 9.9.9"),
            "extra": (SKILL_FILE, lambda text: text + "\nMore.\n", "changes only release files"),
        }
        for name, (path, edit, message) in cases.items():
            with self.subTest(name=name):
                self.git("reset", "--hard", prepared)
                self.write(path, edit(self.read(path)))
                self.commit(name)
                result = self.run_script("verify-release-prep", "--base-ref", "main", check=False)
                self.assertEqual(result.returncode, 1)
                self.assertIn(message, result.stderr)
                self.assertIn("prepare skills --date 2026-11-12", result.stderr)

    def test_verify_rejects_a_base_that_gained_a_fragment(self) -> None:
        self.land_fragments("fix-000001")
        self.prepare_branch("2026-11-12")
        self.git("switch", "main")
        self.land_fragments("late-000002")
        self.git("switch", "-c", "merge-ref")
        self.git("merge", "--no-ff", "--no-edit", "release-prep/skills")

        result = self.check_pr(base_ref="HEAD^1", labels="release-prep")

        self.assertEqual(result.returncode, 1)
        self.assertIn("does not consume: changelog.d/skills/late-000002.md", result.stderr)
        self.assertIn("python3 script/release/changelog_fragments.py prepare skills --date 2026-11-12", result.stderr)

    def test_release_file_edits_need_release_prep_a_correction_line_or_grace(self) -> None:
        self.git("switch", "-c", "edit-version")
        self.write("skills/VERSION", "9.9.9\n")
        self.commit("edit version")

        result = self.check_pr()
        self.assertEqual(result.returncode, 1)
        self.assertIn("only a release-prep pull request changes these release files: skills/VERSION", result.stderr)
        result = self.check_pr(pr_number=LEGACY_PR_NUMBER)
        self.assertEqual(result.returncode, 1)
        self.assertIn("must change both skills/VERSION and skills/CHANGELOG.md", result.stderr)
        self.set_body("## Summary\n\nchangelog: correction: restore the released version\n")
        self.assertEqual(self.check_pr().returncode, 0)

    def test_manifest_edits_are_release_file_edits(self) -> None:
        self.git("switch", "-c", "edit-manifest")
        path = "skills/.codex-plugin/plugin.json"
        self.write(path, self.read(path).replace("Super Durable", "Super  Durable", 1))
        self.commit("edit manifest")
        self.assertIn(path, self.check_pr().stderr)

    def test_skill_content_needs_a_fragment_or_the_no_bump_line(self) -> None:
        self.git("switch", "-c", "edit-skill")
        self.write(SKILL_FILE, self.read(SKILL_FILE) + "\nA new rule.\n")
        self.commit("edit skill")

        result = self.check_pr()
        self.assertEqual(result.returncode, 1)
        self.assertIn("without a skills fragment", result.stderr)
        self.set_body("skills-release: no bump needed: wording only\n")
        self.assertEqual(self.check_pr().returncode, 0)

        self.set_body("## Summary\n")
        self.write("changelog.d/skills/edit-skill-abc123.md", "---\nbump: patch\n---\n- Add a rule.\n")
        self.commit("add fragment")
        self.assertEqual(self.check_pr().returncode, 0)

    def test_a_grace_pull_request_may_bump_the_old_way(self) -> None:
        self.git("switch", "-c", "legacy-bump")
        self.write(SKILL_FILE, self.read(SKILL_FILE) + "\nA new rule.\n")
        self.legacy_bump(self.next_patch(self.base_version))
        self.commit("legacy bump")

        result = self.check_pr(pr_number=LEGACY_PR_NUMBER)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.check_pr().returncode, 1)

    def test_a_grace_pull_request_must_raise_the_version_and_add_its_entry(self) -> None:
        self.land_fragments("fix-000001")
        self.prepare_branch("2026-11-12")
        self.git("switch", "main")
        self.git("merge", "-q", "--ff-only", "release-prep/skills")
        released = self.read("skills/VERSION").strip()
        cases = {
            "reverts": (self.base_version, None, f"above the base's {released}, not set {self.base_version}"),
            "keeps": (released, None, "must change both skills/VERSION and skills/CHANGELOG.md"),
            "no entry": (self.next_patch(released), "", "must add a new newest"),
            "wrong entry": (self.next_patch(released), "9.9.9", "must add a new newest"),
        }
        for name, (version, entry_version, message) in cases.items():
            with self.subTest(name=name):
                self.git("switch", "-q", "-C", "stale-legacy", "main")
                self.write(SKILL_FILE, self.read(SKILL_FILE) + "\nA new rule.\n")
                self.legacy_bump(version, entry_version)
                self.commit(name)
                result = self.check_pr(pr_number=LEGACY_PR_NUMBER)
                self.assertEqual(result.returncode, 1)
                self.assertIn(message, result.stderr)

    def test_a_grace_pull_request_cannot_bump_over_pending_fragments(self) -> None:
        self.land_fragments("fix-000001")
        self.git("switch", "-c", "legacy-over-fragments")
        self.write(SKILL_FILE, self.read(SKILL_FILE) + "\nA new rule.\n")
        self.legacy_bump(self.next_patch(self.base_version))
        self.commit("legacy bump over fragments")

        result = self.check_pr(pr_number=LEGACY_PR_NUMBER)

        self.assertEqual(result.returncode, 1)
        self.assertIn("unreleased skills fragments exist: changelog.d/skills/fix-000001.md", result.stderr)

    def test_a_grace_pull_request_cannot_bump_and_add_a_fragment(self) -> None:
        self.git("switch", "-c", "legacy-with-fragment")
        self.write(SKILL_FILE, self.read(SKILL_FILE) + "\nA new rule.\n")
        self.legacy_bump(self.next_patch(self.base_version))
        self.write("changelog.d/skills/legacy-with-fragment-abc123.md", "---\nbump: patch\n---\n- Add a rule.\n")
        self.commit("legacy bump with a fragment")

        result = self.check_pr(pr_number=LEGACY_PR_NUMBER)

        self.assertEqual(result.returncode, 1)
        self.assertIn("unreleased skills fragments exist: changelog.d/skills/legacy-with-fragment-abc123.md", result.stderr)

    def test_every_published_skills_file_needs_a_fragment(self) -> None:
        for path in ("skills/README.md", "skills/script/check-package.py", "skills/hooks/new-hook.sh"):
            with self.subTest(path=path):
                self.git("switch", "-q", "-C", "edit-published", "main")
                self.write(path, (self.read(path) if (self.repository / path).exists() else "") + "\n# More.\n")
                self.commit("edit " + path)
                result = self.check_pr()
                self.assertEqual(result.returncode, 1)
                self.assertIn("without a skills fragment", result.stderr)

    def test_the_archived_change_log_needs_no_fragment(self) -> None:
        self.git("switch", "-c", "edit-archive")
        path = "skills/CHANGELOG-0.x.md"
        self.write(path, self.read(path) + "\n")
        self.commit("edit archive")
        result = self.check_pr()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_a_release_prep_label_on_a_normal_pull_request_fails(self) -> None:
        self.land_fragments("fix-000001")
        self.git("switch", "-c", "labelled")
        self.write(SKILL_FILE, self.read(SKILL_FILE) + "\nA new rule.\n")
        self.write("changelog.d/skills/labelled-abc123.md", "---\nbump: patch\n---\n- Add a rule.\n")
        self.commit("labelled")

        result = self.check_pr(labels="docs,release-prep")
        self.assertEqual(result.returncode, 1)
        self.assertIn("changes only release files", result.stderr)

    def test_base_fragments_change_only_with_a_correction_line(self) -> None:
        self.land_fragments("fix-000001")
        self.git("switch", "-c", "drop-fragment")
        self.git("rm", "-q", "changelog.d/skills/fix-000001.md")
        self.commit("drop fragment")

        result = self.check_pr()
        self.assertEqual(result.returncode, 1)
        self.assertIn("fragments already on the base", result.stderr)
        self.set_body("changelog: correction: the change was reverted\n")
        self.assertEqual(self.check_pr().returncode, 0)

    def test_check_pr_diffs_a_merge_commit_in_a_depth_two_clone(self) -> None:
        self.git("switch", "-c", "feature")
        self.write(SKILL_FILE, self.read(SKILL_FILE) + "\nA new rule.\n")
        self.commit("edit skill")
        self.git("switch", "main")
        self.write("skills/README.md", self.read("skills/README.md") + "\nMain moved.\n")
        self.commit("main moved")
        self.git("merge", "--no-ff", "--no-edit", "feature")
        clone = Path(self.temporary_directory.name) / "clone"
        subprocess.run(
            ("git", "clone", "-q", "--depth", "2", f"file://{self.repository}", str(clone)),
            check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )

        result = self.run_script(
            "check-pr", "--base-ref", "HEAD^1", "--pr-body-file", str(self.body_file), "--pr-number", NEW_PR_NUMBER,
            cwd=clone, check=False,
        )

        self.assertEqual(result.returncode, 1)
        self.assertIn("without a skills fragment", result.stderr)
        self.assertNotIn("README.md", result.stderr)

    def next_patch(self, version: str) -> str:
        major, minor, patch = (int(part) for part in version.split("."))
        return f"{major}.{minor}.{patch + 1}"

    def legacy_bump(self, version: str, entry_version: str | None = None) -> None:
        """Bumps every version copy the old way; entry_version overrides the entry heading, and "" omits it."""
        previous = self.read("skills/VERSION").strip()
        for path in ("skills/VERSION", "skills/dex-sdk/VERSION"):
            self.write(path, f"{version}\n")
        for path in MANIFESTS:
            self.write(path, self.read(path).replace(f'"version": "{previous}"', f'"version": "{version}"'))
        changelog = self.read("skills/CHANGELOG.md")
        heading = "" if entry_version == "" else f"## {entry_version or version} - 2026-11-12\n\n- Legacy bump.\n\n"
        newest = changelog.index("\n## ") + 1
        self.write("skills/CHANGELOG.md", changelog[:newest] + heading + changelog[newest:] + ("\n" if not heading else ""))

    def land_fragments(self, *stems: str) -> None:
        for stem in stems:
            self.write(f"changelog.d/skills/{stem}.md", f"---\nbump: patch\n---\n- Fix {stem}.\n")
        self.commit("land " + " ".join(stems))

    def prepare_branch(self, release_date: str) -> None:
        self.git("switch", "-c", "release-prep/skills")
        self.run_script("prepare", "skills", "--date", release_date)
        self.commit("prepare skills")

    def check_pr(
        self, base_ref: str = "main", labels: str = "", pr_number: str = NEW_PR_NUMBER
    ) -> subprocess.CompletedProcess[str]:
        return self.run_script(
            "check-pr", "--base-ref", base_ref, "--pr-body-file", str(self.body_file), "--pr-number", pr_number,
            "--labels", labels, check=False,
        )

    def run_check_package(self) -> None:
        self.commit("prepared")
        result = subprocess.run(
            (sys.executable, "script/check-package.py"),
            cwd=self.repository / "skills", text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def run_script(self, *arguments: str, cwd: Path | None = None, check: bool = True) -> subprocess.CompletedProcess[str]:
        result = subprocess.run(
            (sys.executable, str(SCRIPT), *arguments),
            cwd=cwd or self.repository, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
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


def load_script_module():
    specification = importlib.util.spec_from_file_location("changelog_fragments_under_test", SCRIPT)
    module = importlib.util.module_from_spec(specification)
    sys.modules[specification.name] = module
    specification.loader.exec_module(module)
    return module


if __name__ == "__main__":
    unittest.main()
