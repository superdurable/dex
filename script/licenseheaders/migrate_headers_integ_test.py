# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("migrate_headers.py").resolve()
TEMPLATES_DIR = SCRIPT.parent


class MigrationIntegrationTest(unittest.TestCase):
    def test_cutoff_lineage_forced_new_dirty_and_idempotence(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.initialize_repository(root)
            cutoff = self.create_cutoff(root)
            contamination = self.create_contamination(root)
            self.create_current_tree(root)
            self.write_policy(root, cutoff, contamination)

            report_result = self.run_migration(root, "--report")
            self.assertEqual(report_result.returncode, 0, report_result.stderr)
            report = self.read_report(root)
            classifications = {
                entry["path"]: entry["classification"] for entry in report["entries"]
            }
            self.assertEqual(classifications["server/legacy.go"], "mixed")
            self.assertEqual(classifications["server/created.go"], "new")
            self.assertEqual(classifications["server/renamed.go"], "legacy-only")
            self.assertEqual(classifications["server/copied.go"], "mixed")
            self.assertEqual(classifications["sdk-rust/src/lib.rs"], "new")
            self.assertEqual(
                classifications["sdk-python/tests/iwfcompat/basic_flow.py"],
                "third-party-mixed",
            )
            self.assertEqual(classifications["web/server.go"], "new")
            self.assertEqual(classifications["web/app.tsx"], "new")
            self.assertEqual(classifications["web/site.css"], "new")
            self.assertEqual(classifications["web/index.html"], "new")
            self.assertEqual(classifications["protos/api.proto"], "new")
            self.assertEqual(classifications["docs/legacy.py"], "excluded")
            self.assertEqual(classifications["examples/go/main.go"], "excluded")

            apply_result = self.run_migration(root, "--apply")
            self.assertEqual(apply_result.returncode, 0, apply_result.stderr)
            mixed = (root / "server/legacy.go").read_text()
            self.assertIn("Copyright (c) 2020 Legacy Holder", mixed)
            self.assertIn("Modifications Copyright (c) 2026 Super Durable, Inc.", mixed)
            third_party_mixed = (
                root / "sdk-python/tests/iwfcompat/basic_flow.py"
            ).read_text()
            self.assertIn("derived from indeedeng/iwf-java-sdk", third_party_mixed)
            self.assertIn("Third-Party Materials remain", third_party_mixed)
            for path in (
                "sdk-rust/src/lib.rs",
                "web/server.go",
                "web/app.tsx",
                "web/site.css",
                "web/index.html",
                "protos/api.proto",
            ):
                self.assertIn(
                    "SPDX-License-Identifier: LicenseRef-Super-Durable-1.0",
                    (root / path).read_text(),
                )

            first_hash = self.tree_hash(root)
            second_apply = self.run_migration(root, "--apply")
            self.assertEqual(second_apply.returncode, 0, second_apply.stderr)
            self.assertEqual(first_hash, self.tree_hash(root))

            dirty_path = root / "server/renamed.go"
            dirty_path.write_text(dirty_path.read_text().replace("return 1", "return 2"))
            dirty_result = self.run_migration(root, "--report")
            self.assertEqual(dirty_result.returncode, 1)
            dirty_report = self.read_report(root)
            manual_paths = {
                entry["path"]
                for entry in dirty_report["entries"]
                if entry["classification"] == "manual-review"
            }
            self.assertEqual(manual_paths, {"server/renamed.go"})

    def initialize_repository(self, root: Path) -> None:
        self.git(root, "init", "-q")
        self.git(root, "config", "user.name", "Migration Test")
        self.git(root, "config", "user.email", "migration@example.com")
        headers_dir = root / "script" / "licenseheaders"
        headers_dir.mkdir(parents=True)
        for name in (
            "apache-2.0.txt",
            "legacy-reference.txt",
            "mit.txt",
            "mixed.txt",
            "super-durable-1.0.txt",
            "third-party-mixed.txt",
        ):
            shutil.copyfile(TEMPLATES_DIR / name, headers_dir / name)

    def create_cutoff(self, root: Path) -> str:
        legacy_header = (
            "// Copyright (c) 2020 Legacy Holder\n"
            "//\n"
            "// Permission is hereby granted under the legacy MIT terms.\n\n"
        )
        self.write(root, "server/legacy.go", legacy_header + "package server\n\nfunc Legacy() int { return 1 }\n")
        self.write(root, "server/unchanged.go", legacy_header + "package server\n\nfunc Unchanged() int { return 1 }\n")
        self.write(root, "docs/legacy.py", "print('legacy docs')\n")
        self.write(root, "examples/go/main.go", legacy_header + "package main\n")
        self.git(root, "add", ".")
        self.git(root, "commit", "-q", "-m", "cutoff")
        return self.git(root, "rev-parse", "HEAD").strip()

    def create_contamination(self, root: Path) -> str:
        path = root / "server/legacy.go"
        synthetic = (
            "// Copyright (c) 2022-2026 Super Durable, Inc.\n"
            "//\n"
            "// Permission is hereby granted under the legacy MIT terms.\n\n"
        )
        path.write_text(synthetic + path.read_text())
        self.git(root, "add", str(path.relative_to(root)))
        self.git(root, "commit", "-q", "-m", "mechanical header")
        return self.git(root, "rev-parse", "HEAD").strip()

    def create_current_tree(self, root: Path) -> None:
        legacy_path = root / "server/legacy.go"
        legacy_path.write_text(legacy_path.read_text().replace("return 1", "return 3"))
        self.write(root, "server/created.go", "package server\n\nfunc Created() {}\n")
        self.git(root, "mv", "server/unchanged.go", "server/renamed.go")
        shutil.copyfile(root / "server/renamed.go", root / "server/copied.go")
        self.write(root, "sdk-rust/src/lib.rs", "pub fn sdk() {}\n")
        self.write(
            root,
            "sdk-python/tests/iwfcompat/basic_flow.py",
            "class BasicFlow:\n    pass\n",
        )
        self.write(root, "web/server.go", "package web\n")
        self.write(root, "web/app.tsx", "export const App = () => null;\n")
        self.write(root, "web/site.css", "body { color: black; }\n")
        self.write(root, "web/index.html", "<!doctype html>\n")
        self.write(root, "protos/api.proto", 'syntax = "proto3";\n')
        self.git(root, "add", ".")
        self.git(root, "commit", "-q", "-m", "current")

    def write_policy(self, root: Path, cutoff: str, contamination: str) -> None:
        policy = {
            "version": 1,
            "cutoff": cutoff,
            "contamination_commit": contamination,
            "included_prefixes": ["server", "sdk-python", "sdk-rust", "web", "protos"],
            "forced_new_prefixes": ["sdk-rust", "web", "protos"],
            "third_party_mixed_prefixes": ["sdk-python/tests/iwfcompat"],
            "excluded_prefixes": ["docs", "examples"],
            "retained_license_prefixes": {
                "examples/go": "mit",
            },
            "reviewed_mixed_paths": [],
            "repair_from_head_paths": [],
            "legacy_licenses": {"server": ["MIT"]},
        }
        policy_path = root / "script" / "licenseheaders" / "policy.json"
        policy_path.write_text(json.dumps(policy, indent=2) + "\n")

    def run_migration(self, root: Path, mode: str) -> subprocess.CompletedProcess[str]:
        environment = os.environ.copy()
        environment["SUPER_DURABLE_REPO_ROOT"] = str(root)
        environment["SUPER_DURABLE_HEADERS_DIR"] = str(root / "script" / "licenseheaders")
        return subprocess.run(
            ["python3", str(SCRIPT), mode],
            cwd=root,
            env=environment,
            check=False,
            capture_output=True,
            text=True,
        )

    def read_report(self, root: Path) -> dict[str, object]:
        report_path = root / "script" / "licenseheaders" / "migration-report.json"
        return json.loads(report_path.read_text())

    def tree_hash(self, root: Path) -> str:
        digest = hashlib.sha256()
        for path in sorted(root.rglob("*")):
            if not path.is_file() or ".git" in path.parts or path.name == "migration-report.json":
                continue
            digest.update(str(path.relative_to(root)).encode())
            digest.update(path.read_bytes())
        return digest.hexdigest()

    def write(self, root: Path, relative_path: str, content: str) -> None:
        path = root / relative_path
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)

    def git(self, root: Path, *arguments: str) -> str:
        return subprocess.check_output(["git", *arguments], cwd=root, text=True)


if __name__ == "__main__":
    unittest.main()
