#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

import importlib.util
import tempfile
import unittest
from pathlib import Path

SPEC = importlib.util.spec_from_file_location(
    "check_package", Path(__file__).resolve().parents[1] / "script/check-package.py",
)
CHECKER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECKER)


class RemovedConfigurationKeyTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.original_root = CHECKER.ROOT
        CHECKER.ROOT = self.root
        self.addCleanup(setattr, CHECKER, "ROOT", self.original_root)
        self.reference = self.root / "dex-sdk/references/core/data-handling.md"
        self.reference.parent.mkdir(parents=True)
        (self.root / "README.md").write_text("Dex Skills\n")

    def test_accepts_guidance_without_removed_keys(self):
        self.reference.write_text("server-zd records each Step method input with its Step execution.\n")
        CHECKER.check_removed_dex_contracts()

    def test_rejects_each_removed_go_server_configuration_key(self):
        for key in ("blobStore", "blobStore.asyncStepInputSnapshotsEnabled", "asyncStepInputSnapshotsEnabled"):
            with self.subTest(key=key):
                self.reference.write_text(f"Enable `{key}` for exact inputs.\n")
                with self.assertRaises(SystemExit) as raised:
                    CHECKER.check_removed_dex_contracts()
                self.assertIn("removed", str(raised.exception))


if __name__ == "__main__":
    unittest.main()
