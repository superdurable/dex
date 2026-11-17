# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Fixture tests for the dexcli dev -open=false check."""

import importlib.util
from pathlib import Path
import tempfile
import unittest

SCRIPT_DIRECTORY = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location(
    "check_dexcli_dev_open_flag", SCRIPT_DIRECTORY / "check_dexcli_dev_open_flag.py"
)
assert SPEC is not None and SPEC.loader is not None
check = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(check)


class DexcliDevOpenFlagTest(unittest.TestCase):
    def scan(self, files: dict[str, str]) -> list[str]:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for path, text in files.items():
                (root / path).parent.mkdir(parents=True, exist_ok=True)
                (root / path).write_text(text, encoding="utf-8")
            return check.find_violations(root, list(files))

    def test_reports_starts_without_open_false(self) -> None:
        files = {
            "a/run.sh": '"$DEXCLI" dev -dex-port 1 \\\n  -web-port 2\n',
            "b/harness.py": 'subprocess.Popen([dexcli, "dev", "-web-port", "2"])\n',
            "c/harness.ts": 'spawn(dexcli, ["dev", "--zd-data-dir", dir]);\n',
            "d/Makefile": "serve:\n\tdexcli dev\n",
        }
        self.assertEqual(self.scan(files), list(files))

    def test_accepts_open_false_and_non_starts(self) -> None:
        files = {
            "a/run.sh": '"$DEXCLI" dev -web-port 2 \\\n  -open=false\n',
            "b/harness.py": 'Popen([dexcli, "dev", "--open=false", "-dex-port", "1"])\n',
            "c/server.py": 'Popen([server_zd, "dev", "--data-dir", d, "--listen", a])\n',
            "d/notes.sh": '# Start dexcli dev first.\necho "uses dexcli dev"\n./dexcli dev --help\n',
            "cli/internal/dev/config.go": 'flags.BoolVar(&open, "open", true, "")\n"dev" "-web-port"\n',
        }
        self.assertEqual(self.scan(files), [])

    def test_repository_harnesses_are_detected(self) -> None:
        root = SCRIPT_DIRECTORY.parent
        for path in ("agents/go/core/agentstest/processes.go", "sdk-go/run-e2e-tests.sh"):
            text = (root / path).read_text(encoding="utf-8")
            self.assertTrue(check.starts_dexcli_dev(path, text), path)


if __name__ == "__main__":
    unittest.main()
