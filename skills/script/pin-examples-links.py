#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

"""Pin Dex example links in a mirror copy to the synced Dex commit."""

import argparse
import os
import re
from pathlib import Path


EXAMPLES_LINK = re.compile(r"(https://github\.com/superdurable/dex/(?:tree|blob)/)main(/examples/)")


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
    replacement = rf"\g<1>{dex_commit}\g<2>"
    for directory, subdirectories, filenames in os.walk(mirror_directory, onerror=raise_walk_error):
        if ".git" in subdirectories:
            subdirectories.remove(".git")
        for filename in filenames:
            path = Path(directory) / filename
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
    return files_changed, links_changed


def raise_walk_error(error: OSError) -> None:
    raise error


if __name__ == "__main__":
    main()
