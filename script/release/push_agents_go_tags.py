# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Pushes the agents Go module tags of a release, agents/go/<module>/v<version>, onto the release commit.

Release changed components runs it in its own job after it creates the agents/v<version> Release, so a
failed push fails only that job, and rerunning the job finishes it:

- a tag that already names the release commit is skipped;
- a tag that names another commit fails the run before anything is pushed. The Go module proxy keeps
  the first commit it serves for a version, so a Go tag never moves; release the next patch version;
- the other tags go up in one atomic push, so they all land or none does.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys

AGENTS_GO_TAG_PATTERN = re.compile(r"^agents/go/[a-z0-9][a-z0-9_-]*/v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$")


class TagPushError(Exception):
    """A tag cannot be pushed onto the release commit."""


def git(*arguments: str) -> str:
    return subprocess.run(("git", *arguments), check=True, capture_output=True, text=True).stdout


def remote_tag_commits(remote: str, tags: list[str]) -> dict[str, str]:
    """Returns the commit each tag that exists on the remote names, peeling annotated tags."""
    patterns = [pattern for tag in tags for pattern in (f"refs/tags/{tag}", f"refs/tags/{tag}^{{}}")]
    named: dict[str, str] = {}
    peeled: dict[str, str] = {}
    for line in git("ls-remote", "--tags", remote, *patterns).splitlines():
        object_name, _, ref = line.partition("\t")
        tag = ref.removeprefix("refs/tags/")
        if tag.endswith("^{}"):
            peeled[tag.removesuffix("^{}")] = object_name
        else:
            named[tag] = object_name
    named.update(peeled)
    return {tag: commit for tag, commit in named.items() if tag in tags}


def push_agents_go_tags(commit: str, tags: list[str], remote: str) -> tuple[str, list[str], list[str]]:
    """Pushes each missing tag onto commit.

    Returns the full release commit, the tags that already named it, and the tags this call pushed.
    """
    invalid = [tag for tag in tags if not AGENTS_GO_TAG_PATTERN.fullmatch(tag)]
    if invalid:
        raise TagPushError("\n".join(f"{tag} is not an agents Go module tag agents/go/<module>/v<version>" for tag in invalid))
    tags = list(dict.fromkeys(tags))
    release_commit = git("rev-parse", "--verify", f"{commit}^{{commit}}").strip()
    existing = remote_tag_commits(remote, tags)
    moved = [tag for tag in tags if existing.get(tag, release_commit) != release_commit]
    if moved:
        raise TagPushError(
            "\n".join(
                f"{tag} names {existing[tag]}, not the release commit {release_commit}. A Go tag never moves, "
                "because the Go module proxy keeps the first commit it serves; release the next patch version"
                for tag in moved
            )
        )
    missing = [tag for tag in tags if tag not in existing]
    if missing:
        git("push", "--atomic", remote, *(f"{release_commit}:refs/tags/{tag}" for tag in missing))
    return release_commit, [tag for tag in tags if tag in existing], missing


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--commit", required=True, help="the release commit, such as GITHUB_SHA")
    parser.add_argument("--remote", default="origin", help="the remote to push to; default origin")
    parser.add_argument("tags", nargs="*", help="agents/go/<module>/v<version> tags, as the release plan prints them")
    arguments = parser.parse_args()
    if not arguments.tags:
        print("no agents Go module tags to push")
        return 0
    try:
        release_commit, already_pushed, pushed = push_agents_go_tags(arguments.commit, arguments.tags, arguments.remote)
    except TagPushError as error:
        print(error, file=sys.stderr)
        return 1
    except subprocess.CalledProcessError as error:
        print(f"{' '.join(error.cmd)} failed:\n{error.stderr}", file=sys.stderr)
        return 1
    for tag in already_pushed:
        print(f"{tag} already names {release_commit}")
    for tag in pushed:
        print(f"pushed {tag} onto {release_commit}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
