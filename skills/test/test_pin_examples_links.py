#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).resolve().parents[1] / "script/pin-examples-links.py"
DEX_REPOSITORY_URL = "https://github.com/superdurable/dex"
DEX_COMMIT = "1234567890abcdef1234567890abcdef12345678"


class PinExamplesLinksTest(unittest.TestCase):
    def test_rewrites_only_example_links_and_counts_each_occurrence(self):
        with tempfile.TemporaryDirectory() as directory:
            mirror = Path(directory)
            tree_link = f"{DEX_REPOSITORY_URL}/tree/main/examples/go"
            blob_link = f"{DEX_REPOSITORY_URL}/blob/main/examples/python/flow.py?plain=1#L10"
            other_links = (
                f"{DEX_REPOSITORY_URL}/blob/main/README.md\r\n"
                f"{DEX_REPOSITORY_URL}/tree/main/examples-other/go\r\n"
                f"{DEX_REPOSITORY_URL}/tree/main/sdk-go/examples/go\r\n"
                f"{DEX_REPOSITORY_URL}/tree/another-ref/examples/go\r\n"
                "https://docs.superdurable.io/reference/go/\r\n"
                "https://github.com/superdurable/other/tree/main/examples/go\r\n"
            )
            document = mirror / "README.md"
            document.write_bytes(f"[Examples]({tree_link})\r\n{other_links}".encode())
            nested = mirror / "nested"
            nested.mkdir()
            settings = nested / "links.yaml"
            settings.write_text(f"flow: {blob_link}\nfolder: {tree_link}\n", encoding="utf-8")
            binary = mirror / "fixture.bin"
            binary_contents = b"\x00" + tree_link.encode()
            binary.write_bytes(binary_contents)
            git_metadata = mirror / ".git"
            git_metadata.mkdir()
            git_config = git_metadata / "config"
            git_config.write_text(tree_link, encoding="utf-8")

            result = self.run_script(mirror, DEX_COMMIT)

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), "Pinned 3 Dex example links across 2 files.")
            pinned_tree_link = tree_link.replace("/main/", f"/{DEX_COMMIT}/")
            pinned_blob_link = blob_link.replace("/main/", f"/{DEX_COMMIT}/")
            self.assertEqual(document.read_bytes(), f"[Examples]({pinned_tree_link})\r\n{other_links}".encode())
            self.assertEqual(settings.read_text(encoding="utf-8"), f"flow: {pinned_blob_link}\nfolder: {pinned_tree_link}\n")
            self.assertEqual(binary.read_bytes(), binary_contents)
            self.assertEqual(git_config.read_text(encoding="utf-8"), tree_link)
            repeat = self.run_script(mirror, DEX_COMMIT)
            self.assertEqual(repeat.returncode, 0, repeat.stderr)
            self.assertEqual(repeat.stdout.strip(), "Pinned 0 Dex example links across 0 files.")

    def test_rejects_invalid_commit_before_rewriting(self):
        with tempfile.TemporaryDirectory() as directory:
            mirror = Path(directory)
            document = mirror / "README.md"
            source = f"{DEX_REPOSITORY_URL}/tree/main/examples/go"
            document.write_text(source, encoding="utf-8")
            for commit in (DEX_COMMIT[:7], DEX_COMMIT.upper(), "z" * 40):
                with self.subTest(commit=commit):
                    result = self.run_script(mirror, commit)
                    self.assertEqual(result.returncode, 2)
                    self.assertIn("Dex commit must be a 40-character lowercase hexadecimal SHA", result.stderr)
                    self.assertEqual(document.read_text(encoding="utf-8"), source)

    @staticmethod
    def run_script(mirror, commit):
        return subprocess.run([sys.executable, str(SCRIPT), str(mirror), commit], capture_output=True, text=True)


if __name__ == "__main__":
    unittest.main()
