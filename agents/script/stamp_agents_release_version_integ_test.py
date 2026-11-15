#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Exercise release stamping and packed npm manifest verification without registry access."""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import stamp_agents_release_version as stamp

SCRIPT_DIRECTORY = Path(__file__).resolve().parent


class AgentsReleaseStampIntegrationTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary_directory.cleanup)
        self.agents_directory = Path(self.temporary_directory.name) / "agents"
        self.script = self.agents_directory / "script" / "stamp_agents_release_version.py"
        self.script.parent.mkdir(parents=True)
        shutil.copy2(SCRIPT_DIRECTORY / self.script.name, self.script)
        for language, packages, filenames in (
            ("python", stamp.PYTHON_PACKAGES, ("pyproject.toml", "uv.lock")),
            ("typescript", stamp.TYPESCRIPT_PACKAGES, ("package.json", "package-lock.json")),
        ):
            for package in packages:
                source = stamp.package_directory(language, package)
                destination = self.agents_directory / source.relative_to(stamp.AGENTS_DIR)
                destination.mkdir(parents=True)
                for filename in filenames:
                    shutil.copy2(source / filename, destination / filename)

    def run_stamp(self, version: str, check: bool = False) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, str(self.script), version, *(["--check"] if check else [])],
            capture_output=True, text=True,
        )

    def test_stamp_and_check_all_packages_preserve_other_constraints(self) -> None:
        manifests = list(self.agents_directory.rglob("package*.json"))
        originals = {path: json.loads(path.read_text()) for path in manifests}
        for version in ("0.6.11", "0.6.12-rc.1"):
            result = self.run_stamp(version)
            self.assertEqual(result.returncode, 0, result.stderr)
            result = self.run_stamp(version, check=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            for path, original in originals.items():
                updated = json.loads(path.read_text())
                self.assertEqual(updated["version"], version)
                original_entries = original.get("packages", {"": original})
                updated_entries = updated.get("packages", {"": updated})
                for key, entry in original_entries.items():
                    stamped_entry = updated_entries[key]
                    if key == "" or (stamp.TYPESCRIPT_LOCAL_PACKAGE_KEY_PATTERN.match(key) and entry.get("resolved", "").startswith("file:")):
                        self.assertEqual(stamped_entry["version"], version)
                    self.assertEqual(entry.get("devDependencies"), stamped_entry.get("devDependencies"))
                    for peer, constraint in entry.get("peerDependencies", {}).items():
                        expected = version if peer == stamp.TYPESCRIPT_CORE_PACKAGE or peer.startswith(
                            f"{stamp.TYPESCRIPT_CORE_PACKAGE}-"
                        ) else constraint
                        self.assertEqual(stamped_entry["peerDependencies"][peer], expected, (path, key, peer))

    def test_check_rejects_sibling_ranges_in_manifests_and_locks(self) -> None:
        for filename in ("package.json", "package-lock.json"):
            with self.subTest(filename=filename):
                self.assertEqual(self.run_stamp("0.6.11").returncode, 0)
                path = self.agents_directory / "cli/typescript/claude-sdk" / filename
                manifest = json.loads(path.read_text())
                entry = manifest if filename == "package.json" else manifest["packages"][""]
                entry["peerDependencies"][stamp.TYPESCRIPT_CORE_PACKAGE] = "^0.6.11"
                path.write_text(json.dumps(manifest, indent=2) + "\n")
                result = self.run_stamp("0.6.11", check=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("not stamped", result.stderr)
                self.assertEqual(json.loads(path.read_text()), manifest)

    def test_packed_command_rejects_every_sibling_range(self) -> None:
        self.assertEqual(self.run_stamp("0.6.11").returncode, 0)
        package_directory = self.agents_directory / "cli/typescript/claude-sdk"
        manifest_path = package_directory / "package.json"
        manifest = json.loads(manifest_path.read_text())
        files = {"LICENSE", "README.md", "dist/src/index.js", "dist/src/index.d.ts"}
        files.update(manifest["bin"].values())
        for target in manifest["exports"].values():
            files.update(target.values())
        for filename in files:
            path = package_directory / filename
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("#!/usr/bin/env node\n" if filename in manifest["bin"].values() else "")
        siblings = [peer for peer in manifest["peerDependencies"] if peer.startswith(stamp.TYPESCRIPT_CORE_PACKAGE)]
        for ranged_peer in (None, *siblings):
            with self.subTest(ranged_peer=ranged_peer):
                for peer in siblings:
                    manifest["peerDependencies"][peer] = "^0.6.11" if peer == ranged_peer else "0.6.11"
                manifest_path.write_text(json.dumps(manifest))
                subprocess.run(
                    ["npm", "pack", "--ignore-scripts", "--offline", "--cache", str(self.agents_directory / "npm-cache")],
                    cwd=package_directory, capture_output=True, text=True, check=True,
                )
                result = subprocess.run(
                    ["node", str(SCRIPT_DIRECTORY / "verify-built-npm-packages.mjs"),
                     str(package_directory), "0.6.11", "cli/claude-sdk"],
                    capture_output=True, text=True,
                )
                if ranged_peer is None:
                    self.assertEqual(result.returncode, 0, result.stderr)
                else:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn(f"{ranged_peer}@0.6.11, not ^0.6.11", result.stderr)


if __name__ == "__main__":
    unittest.main()
