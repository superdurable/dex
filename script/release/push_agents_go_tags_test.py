# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("push_agents_go_tags.py")
CORE_TAG = "agents/go/core/v0.6.16"
EINO_TAG = "agents/go/eino/v0.6.16"


class PushAgentsGoTagsTest(unittest.TestCase):
    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.remote = Path(temporary_directory.name) / "remote.git"
        self.checkout = Path(temporary_directory.name) / "checkout"
        subprocess.run(("git", "init", "--quiet", "--bare", str(self.remote)), check=True)
        self.checkout.mkdir()
        self.git("init", "--quiet", "-b", "main")
        self.git("config", "user.name", "Release Test")
        self.git("config", "user.email", "release-test@example.com")
        self.git("remote", "add", "origin", str(self.remote))
        self.git("commit", "--quiet", "--allow-empty", "-m", "before the release")
        self.other_commit = self.git("rev-parse", "HEAD")
        self.git("commit", "--quiet", "--allow-empty", "-m", "release")
        self.release_commit = self.git("rev-parse", "HEAD")
        self.git("push", "--quiet", "origin", "main")

    def git(self, *arguments: str) -> str:
        return subprocess.run(
            ("git", *arguments), cwd=self.checkout, check=True, capture_output=True, text=True
        ).stdout.strip()

    def push(self, *tags: str, commit: str | None = None) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            (sys.executable, str(SCRIPT), "--commit", commit or self.release_commit, *tags),
            cwd=self.checkout,
            check=False,
            capture_output=True,
            text=True,
        )

    def remote_tags(self) -> dict[str, str]:
        lines = self.git("ls-remote", "--tags", "origin").splitlines()
        return {ref.removeprefix("refs/tags/"): name for name, ref in (line.split("\t") for line in lines)}

    def test_pushes_every_tag_onto_the_release_commit(self) -> None:
        result = self.push(CORE_TAG, EINO_TAG)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.remote_tags(), {CORE_TAG: self.release_commit, EINO_TAG: self.release_commit})
        self.assertIn(f"pushed {CORE_TAG}", result.stdout)
        self.assertIn(f"pushed {EINO_TAG}", result.stdout)

    def test_a_rerun_pushes_only_the_missing_tags(self) -> None:
        self.git("push", "--quiet", "origin", f"{self.release_commit}:refs/tags/{CORE_TAG}")
        result = self.push(CORE_TAG, EINO_TAG)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"{CORE_TAG} already names {self.release_commit}", result.stdout)
        self.assertIn(f"pushed {EINO_TAG}", result.stdout)
        result = self.push(CORE_TAG, EINO_TAG)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("pushed", result.stdout)
        self.assertEqual(self.remote_tags(), {CORE_TAG: self.release_commit, EINO_TAG: self.release_commit})

    def test_an_annotated_tag_on_the_release_commit_counts_as_pushed(self) -> None:
        self.git("tag", "--annotate", "--message", "core", CORE_TAG, self.release_commit)
        self.git("push", "--quiet", "origin", f"refs/tags/{CORE_TAG}")
        result = self.push(CORE_TAG, EINO_TAG)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"{CORE_TAG} already names {self.release_commit}", result.stdout)
        self.assertEqual(self.remote_tags()[EINO_TAG], self.release_commit)

    def test_a_tag_on_another_commit_fails_before_any_push(self) -> None:
        for annotated in (False, True):
            with self.subTest(annotated=annotated):
                self.git("tag", "--force", *(("--annotate", "--message", "eino") if annotated else ()), EINO_TAG,
                         self.other_commit)
                self.git("push", "--quiet", "--force", "origin", f"refs/tags/{EINO_TAG}")
                result = self.push(CORE_TAG, EINO_TAG)
                self.assertEqual(result.returncode, 1)
                self.assertIn(f"{EINO_TAG} names {self.other_commit}, not the release commit", result.stderr)
                self.assertIn("next patch version", result.stderr)
                self.assertNotIn(CORE_TAG, self.remote_tags())

    def test_the_tags_land_together_or_not_at_all(self) -> None:
        hook = self.remote / "hooks/update"
        hook.write_text(f'#!/bin/sh\n[ "$1" = "refs/tags/{EINO_TAG}" ] && exit 1\nexit 0\n', encoding="utf-8")
        hook.chmod(0o755)
        result = self.push(CORE_TAG, EINO_TAG)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(self.remote_tags(), {})
        hook.unlink()
        result = self.push(CORE_TAG, EINO_TAG)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(set(self.remote_tags()), {CORE_TAG, EINO_TAG})

    def test_only_agents_go_module_tags_are_pushed(self) -> None:
        for tag in ("agents/v0.6.16", "sdk-go/v1.45.6", "agents/go/core/0.6.16", "agents/go/core/v0.6.16:main"):
            with self.subTest(tag=tag):
                result = self.push(CORE_TAG, tag)
                self.assertEqual(result.returncode, 1)
                self.assertIn(f"{tag} is not an agents Go module tag", result.stderr)
                self.assertEqual(self.remote_tags(), {})

    def test_no_tags_push_nothing(self) -> None:
        result = self.push()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("no agents Go module tags", result.stdout)
        self.assertEqual(self.remote_tags(), {})


if __name__ == "__main__":
    unittest.main()
