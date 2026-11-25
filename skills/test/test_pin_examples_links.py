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
            skill = mirror / "dex-sdk"
            skill.mkdir()
            document = skill / "SKILL.md"
            document.write_bytes(f"[Examples]({tree_link})\r\n{other_links}".encode())
            nested = skill / "references"
            nested.mkdir()
            settings = nested / "links.yaml"
            settings.write_text(f"flow: {blob_link}\nfolder: {tree_link}\n", encoding="utf-8")
            binary = skill / "fixture.bin"
            binary_contents = b"\x00" + tree_link.encode()
            binary.write_bytes(binary_contents)
            git_metadata = skill / ".git"
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

    def test_pins_example_root_links_at_supported_boundaries(self):
        source = (
            "[Examples](https://github.com/superdurable/dex/tree/main/examples)\n"
            "https://github.com/superdurable/dex/tree/main/examples#readme\n"
            "https://github.com/superdurable/dex/tree/main/examples?plain=1\n"
            "https://github.com/superdurable/dex/tree/main/examples links\n"
            "'https://github.com/superdurable/dex/tree/main/examples'\n"
            '"https://github.com/superdurable/dex/tree/main/examples"\n'
            "https://github.com/superdurable/dex/tree/main/examples\n"
            "https://github.com/superdurable/dex/tree/main/examples"
        )
        with tempfile.TemporaryDirectory() as directory:
            mirror = Path(directory)
            document = self.write_fixture(mirror, "dex-app-builder/SKILL.md", source)

            result = self.run_script(mirror, DEX_COMMIT)

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), "Pinned 8 Dex example links across 1 files.")
            self.assertEqual(document.read_text(encoding="utf-8"), source.replace("/main/", f"/{DEX_COMMIT}/"))

    def test_reports_all_unpinned_example_link_locations(self):
        with tempfile.TemporaryDirectory() as directory:
            mirror = Path(directory)
            valid_link = "https://github.com/superdurable/dex/tree/main/examples/go"
            document = self.write_fixture(
                mirror,
                "dex-sdk/SKILL.md",
                f"{valid_link}\n[Examples](http://github.com/superdurable/dex/tree/main/examples)\n"
                "[Example](ftp://GitHub.com/SUPERDURABLE/DEX/BLOB/MAIN/EXAMPLES/flow.py)\n",
            )
            self.write_fixture(
                mirror,
                "dex-connector-contributor/SKILL.md",
                "Examples\n\n//GitHub.com/superdurable/dex/tree/main/examples/\n",
            )

            result = self.run_script(mirror, DEX_COMMIT)

            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(result.stderr.splitlines(), [
                "dex-connector-contributor/SKILL.md:3: unpinned Dex example link",
                "dex-sdk/SKILL.md:2: unpinned Dex example link",
                "dex-sdk/SKILL.md:3: unpinned Dex example link",
            ])
            self.assertEqual(
                document.read_text(encoding="utf-8").splitlines()[0],
                valid_link.replace("/main/", f"/{DEX_COMMIT}/"),
            )

    def test_walks_only_the_three_skill_directories(self):
        with tempfile.TemporaryDirectory() as directory:
            mirror = Path(directory)
            source = "https://github.com/superdurable/dex/tree/main/examples/go"
            documents = [
                self.write_fixture(mirror, f"{skill}/SKILL.md", source)
                for skill in ("dex-app-builder", "dex-sdk", "dex-connector-contributor")
            ]
            other_source = f"{source}\nhttp://GitHub.com/superdurable/dex/tree/main/examples\n"
            other_files = [
                self.write_fixture(mirror, path, other_source)
                for path in ("script/fixture.py", "test/fixture.py", "CHANGELOG.md", "CHANGELOG-0.x.md", "other/SKILL.md")
            ]

            result = self.run_script(mirror, DEX_COMMIT)

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), "Pinned 3 Dex example links across 3 files.")
            for document in documents:
                self.assertEqual(document.read_text(encoding="utf-8"), source.replace("/main/", f"/{DEX_COMMIT}/"))
            for path in other_files:
                self.assertEqual(path.read_text(encoding="utf-8"), other_source)

    def test_rejects_invalid_commit_before_rewriting(self):
        with tempfile.TemporaryDirectory() as directory:
            mirror = Path(directory)
            source = f"{DEX_REPOSITORY_URL}/tree/main/examples/go"
            document = self.write_fixture(mirror, "dex-sdk/SKILL.md", source)
            for commit in (DEX_COMMIT[:7], DEX_COMMIT.upper(), "z" * 40):
                with self.subTest(commit=commit):
                    result = self.run_script(mirror, commit)
                    self.assertEqual(result.returncode, 2)
                    self.assertIn("Dex commit must be a 40-character lowercase hexadecimal SHA", result.stderr)
                    self.assertEqual(document.read_text(encoding="utf-8"), source)

    @staticmethod
    def write_fixture(mirror, relative_path, content):
        path = mirror / relative_path
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
        return path

    @staticmethod
    def run_script(mirror, commit):
        return subprocess.run([sys.executable, str(SCRIPT), str(mirror), commit], capture_output=True, text=True)


if __name__ == "__main__":
    unittest.main()
