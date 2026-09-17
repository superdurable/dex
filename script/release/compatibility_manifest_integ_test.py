# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Any


SCRIPT = Path(__file__).with_name("compatibility_manifest.py")
SPEC = importlib.util.spec_from_file_location("compatibility_manifest", SCRIPT)
assert SPEC and SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)

REPOSITORY = SCRIPT.parents[2]
VERSION = "0.9.0"
SOURCE_COMMIT = "e93b803a829735292af8c81a0cc1c98b12aee7f7"
SERVER_DIGEST = "sha256:fdaeafe20ed7685f4a1eb8a5acedf2fb48feade2e5ca99b1c3a71a705893e1f0"
CHECKSUMS = {
    "dexcli_v0.9.0_darwin_amd64.tar.gz": "071f530422e869554b2e2a2dc10ce5d917e1093a38a5af4e1438192e9c532408",
    "dexcli_v0.9.0_darwin_arm64.tar.gz": "4ee2df39d0218169b5fe0fc581e9cac2c1f40e24a011ac5c5ba441eccdfd1f51",
    "dexcli_v0.9.0_linux_amd64.tar.gz": "0df459cdde367191e7c962b819a1491073b614da93f5459129f38f90970a7016",
    "dexcli_v0.9.0_linux_arm64.tar.gz": "68f5771cde6ae4a1cfb8c78efb35881765273d4727d6353de41d6b4252476d67",
}


class CompatibilityManifestIntegrationTest(unittest.TestCase):
    def write_checksums(self, directory: Path, checksums: dict[str, str] | None = None) -> Path:
        path = directory / "checksums.txt"
        values = checksums or CHECKSUMS
        path.write_text(
            "".join(f"{checksum}  ./{name}\n" for name, checksum in values.items()),
            encoding="utf-8",
        )
        return path

    def declaration(self) -> dict[str, Any]:
        return json.loads(
            (REPOSITORY / "release" / "compatibility" / f"{VERSION}.json").read_text(
                encoding="utf-8"
            )
        )

    def test_builds_manifest_from_released_component_tags(self) -> None:
        with tempfile.TemporaryDirectory() as directory_name:
            directory = Path(directory_name)
            manifest = MODULE.build_manifest(
                VERSION,
                REPOSITORY / "release" / "compatibility" / f"{VERSION}.json",
                SERVER_DIGEST,
                self.write_checksums(directory),
                False,
            )
        self.assertEqual(manifest["sourceCommit"], SOURCE_COMMIT)
        self.assertEqual(
            manifest["components"]["server"]["image"],
            "docker.io/superdurable/dex-server@" + SERVER_DIGEST,
        )
        self.assertEqual(manifest["components"]["cli"]["checksums"], CHECKSUMS)
        self.assertEqual(manifest["protocol"]["server"], {"minimum": 1, "maximum": 1})
        self.assertEqual(set(manifest["components"]), {component.key for component in MODULE.COMPONENTS})

    def test_rejects_protocol_declaration_drift(self) -> None:
        declaration = self.declaration()
        declaration["protocol"]["clients"]["sdkGo"]["maximum"] = 2
        with tempfile.TemporaryDirectory() as directory_name:
            directory = Path(directory_name)
            declaration_path = directory / "declaration.json"
            declaration_path.write_text(json.dumps(declaration), encoding="utf-8")
            with self.assertRaisesRegex(MODULE.ManifestError, "does not match sdkGo source"):
                MODULE.build_manifest(
                    VERSION,
                    declaration_path,
                    SERVER_DIGEST,
                    self.write_checksums(directory),
                    False,
                )

    def test_rejects_missing_cli_archive(self) -> None:
        checksums = dict(CHECKSUMS)
        checksums.pop("dexcli_v0.9.0_linux_arm64.tar.gz")
        with tempfile.TemporaryDirectory() as directory_name:
            directory = Path(directory_name)
            with self.assertRaisesRegex(MODULE.ManifestError, "exactly four"):
                MODULE.build_manifest(
                    VERSION,
                    REPOSITORY / "release" / "compatibility" / f"{VERSION}.json",
                    SERVER_DIGEST,
                    self.write_checksums(directory, checksums),
                    False,
                )

    def test_rejects_mutable_server_image(self) -> None:
        with tempfile.TemporaryDirectory() as directory_name:
            directory = Path(directory_name)
            with self.assertRaisesRegex(MODULE.ManifestError, "Server image digest"):
                MODULE.build_manifest(
                    VERSION,
                    REPOSITORY / "release" / "compatibility" / f"{VERSION}.json",
                    "v0.9.0",
                    self.write_checksums(directory),
                    False,
                )


if __name__ == "__main__":
    unittest.main()
