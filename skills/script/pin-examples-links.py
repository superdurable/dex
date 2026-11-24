#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

"""Pin Dex example links in a mirror copy to the synced Dex commit."""

import argparse
import os
import re
from pathlib import Path


SKILL_DIRECTORIES = {"dex-app-builder", "dex-sdk", "dex-connector-contributor"}
EXAMPLES_LINK = re.compile(
    r"""(https://github\.com/superdurable/dex/(?:tree|blob)/)main(?=/examples(?:/|[)#?\s'"]|$))"""
)
UNPINNED_EXAMPLES_LINK = re.compile(r"superdurable/dex/(?:tree|blob)/main/examples(?![\w-])", re.IGNORECASE)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mirror_directory", type=Path)
    parser.add_argument("dex_commit")
    arguments = parser.parse_args()
    if not re.fullmatch(r"[0-9a-f]{40}", arguments.dex_commit):
        parser.error("Dex commit must be a 40-character lowercase hexadecimal SHA")
    if not arguments.mirror_directory.is_dir():
        parser.error("mirror directory must exist")

    files_changed, links_changed = pin_examples_links(arguments.mirror_directory, arguments.dex_commit)
    print(f"Pinned {links_changed} Dex example links across {files_changed} files.")


def pin_examples_links(mirror_directory: Path, dex_commit: str) -> tuple[int, int]:
    files_changed = 0
    links_changed = 0
    unpinned_locations = []
    replacement = rf"\g<1>{dex_commit}"
    for directory_name, subdirectories, filenames in os.walk(mirror_directory, onerror=raise_walk_error):
        directory = Path(directory_name)
        if directory == mirror_directory:
            subdirectories[:] = sorted(SKILL_DIRECTORIES.intersection(subdirectories))
            continue
        if ".git" in subdirectories:
            subdirectories.remove(".git")
        for filename in filenames:
            path = directory / filename
            if filename == ".git" or path.is_symlink() or not path.is_file():
                continue
            contents = path.read_bytes()
            if b"\0" in contents:
                continue
            try:
                text = contents.decode("utf-8")
            except UnicodeDecodeError:
                continue
            rewritten, count = EXAMPLES_LINK.subn(replacement, text)
            if count:
                path.write_bytes(rewritten.encode("utf-8"))
                files_changed += 1
                links_changed += count
            if UNPINNED_EXAMPLES_LINK.search(rewritten):
                unpinned_locations.extend(
                    f"{path.relative_to(mirror_directory)}:{line_number}: unpinned Dex example link"
                    for line_number, line in enumerate(rewritten.splitlines(), start=1)
                    if UNPINNED_EXAMPLES_LINK.search(line)
                )
    if unpinned_locations:
        raise SystemExit("\n".join(sorted(unpinned_locations)))
    return files_changed, links_changed


def raise_walk_error(error: OSError) -> None:
    raise error


if __name__ == "__main__":
    main()
