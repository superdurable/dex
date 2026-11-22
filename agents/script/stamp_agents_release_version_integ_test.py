#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Exercise release stamping, Go module stamping, and packed npm manifest verification without registry access."""

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
CORE_MODULE = "github.com/superdurable/dex/agents/go/core"
# An adapter-shaped module: a block require of the core beside a released SDK, and the repository's replace.
GO_FIXTURE = """module github.com/superdurable/dex/agents/go/fixture

go 1.26.0

require (
\tgithub.com/superdurable/dex/agents/go/core v0.1.0
\tgithub.com/superdurable/dex/sdk-go v1.45.6
)

require github.com/stretchr/testify v1.11.1 // indirect

replace github.com/superdurable/dex/agents/go/core => ../core
"""
# A second module: one-line requires of two agents modules, one marked indirect, and a replace block.
GO_SECOND_FIXTURE = """module github.com/superdurable/dex/agents/go/second

go 1.26.0

require github.com/superdurable/dex/agents/go/fixture v0.1.0

require github.com/superdurable/dex/agents/go/core v0.1.0 // indirect

replace (
\tgithub.com/superdurable/dex/agents/go/core => ../core
\tgithub.com/superdurable/dex/agents/go/fixture => ../fixture
)
"""


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
        # The repository's Go modules as they are; today none requires another agents module.
        self.repository_go_mods = sorted(stamp.GO_MODULES_DIR.glob("*/go.mod"))
        self.assertTrue(self.repository_go_mods)
        for go_mod in self.repository_go_mods:
            destination = self.agents_directory / go_mod.relative_to(stamp.AGENTS_DIR)
            destination.parent.mkdir(parents=True)
            shutil.copy2(go_mod, destination)

    def run_stamp(self, version: str, check: bool = False, go: bool = False) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, str(self.script), version, *(["--check"] if check else []), *(["--go"] if go else [])],
            capture_output=True, text=True,
        )

    def write_go_module(self, directory: str, text: str) -> Path:
        path = self.agents_directory / "go" / directory / "go.mod"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
        return path

    def go_mod_texts(self) -> dict[Path, str]:
        return {path: path.read_text() for path in sorted((self.agents_directory / "go").glob("*/go.mod"))}

    def test_go_stamp_leaves_the_repository_go_modules_unchanged(self) -> None:
        originals = self.go_mod_texts()
        self.assertEqual(len(originals), len(self.repository_go_mods))
        for version in ("0.6.16", "0.7.0-rc.1"):
            for check in (False, True):
                result = self.run_stamp(version, check=check, go=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(self.go_mod_texts(), originals)

    def test_go_stamp_requires_agents_modules_at_the_release_version(self) -> None:
        fixture = self.write_go_module("fixture", GO_FIXTURE)
        second = self.write_go_module("second", GO_SECOND_FIXTURE)
        core_text = (self.agents_directory / "go/core/go.mod").read_text()
        for version in ("0.6.16", "0.7.0-rc.1"):
            with self.subTest(version=version):
                result = self.run_stamp(version, go=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(fixture.read_text(), GO_FIXTURE.replace("core v0.1.0", f"core v{version}"))
                self.assertEqual(
                    second.read_text(),
                    GO_SECOND_FIXTURE.replace("core v0.1.0", f"core v{version}").replace(
                        "fixture v0.1.0", f"fixture v{version}"
                    ),
                )
                self.assertEqual((self.agents_directory / "go/core/go.mod").read_text(), core_text)
                result = self.run_stamp(version, check=True, go=True)
                self.assertEqual(result.returncode, 0, result.stderr)
        stamped = self.go_mod_texts()
        result = self.run_stamp("0.6.16", check=True, go=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(f"{fixture}: not stamped", result.stderr)
        self.assertEqual(self.go_mod_texts(), stamped)

    def test_package_stamp_leaves_go_modules_to_the_go_stamp(self) -> None:
        fixture = self.write_go_module("fixture", GO_FIXTURE)
        result = self.run_stamp("0.6.16")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(fixture.read_text(), GO_FIXTURE)
        result = self.run_stamp("0.6.16", check=True)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_go_stamp_refuses_a_require_without_its_local_replace(self) -> None:
        # "second" sorts after "fixture", so its refusal proves that the stamp wrote nothing first.
        local_replace = f"replace {CORE_MODULE} => ../core\n"
        for broken_replace in ("", f"replace {CORE_MODULE} v0.1.0 => ../core\n", f"replace {CORE_MODULE} => ../../core\n",
                               local_replace + f"replace {CORE_MODULE} v0.1.0 => ../core\n"):
            with self.subTest(broken_replace=broken_replace):
                self.write_go_module("fixture", GO_FIXTURE)
                self.write_go_module(
                    "second",
                    f"module github.com/superdurable/dex/agents/go/second\n\ngo 1.26.0\n\nrequire {CORE_MODULE} v0.1.0\n\n"
                    + broken_replace,
                )
                originals = self.go_mod_texts()
                result = self.run_stamp("0.6.16", go=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(f"without the one replace {CORE_MODULE} => ../core", result.stderr)
                self.assertEqual(self.go_mod_texts(), originals)

    def test_go_stamp_refuses_modules_its_tags_cannot_resolve(self) -> None:
        cases = {
            "module github.com/superdurable/dex/agents/go/other\n": "module is github.com/superdurable/dex/agents/go/other",
            (
                "module github.com/superdurable/dex/agents/go/fixture\n\n"
                "require github.com/superdurable/dex/agents/go/missing v0.1.0\n"
            ): "requires github.com/superdurable/dex/agents/go/missing, which is not a module in",
        }
        for text, message in cases.items():
            with self.subTest(message=message):
                self.write_go_module("fixture", text)
                result = self.run_stamp("0.6.16", go=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stderr)
                self.assertEqual((self.agents_directory / "go/fixture/go.mod").read_text(), text)

    def test_go_stamp_refuses_versions_go_cannot_tag(self) -> None:
        fixture = self.write_go_module("fixture", GO_FIXTURE)
        for version, message in (
            ("0.7.0.1", "is not a Go module release version"),
            ("0.7.0-rc.01", "is not a Go module release version"),
            ("0.6.16-0.20261121000000-abcdef123456", "is not a Go module release version"),
            ("2.0.0", "no /v2 suffix"),
        ):
            with self.subTest(version=version):
                result = self.run_stamp(version, go=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stderr)
                self.assertEqual(fixture.read_text(), GO_FIXTURE)

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
