# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

from component_release_notes import ReleaseNotesError, build_component_release_notes, component_changelog_path
from changelog_fragments import TreeReader, release_readiness_errors
from check_agents_go_modules import go_module_errors, go_module_tags

VERSION_PATTERN = re.compile(
    r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
    r"(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$"
)


@dataclass(frozen=True)
class Component:
    key: str
    name: str
    tag_prefix: str
    baseline_patterns: tuple[str, ...]
    paths: tuple[str, ...]


SHARED_RUST_PATHS = (
    "sdk-rust/Cargo.toml",
    "sdk-rust/Cargo.lock",
    "sdk-rust/crates/dex-blob-cache",
)

COMPONENTS = (
    Component("go", "Go SDK", "sdk-go/v", ("sdk-go/v*",), ("sdk-go",)),
    Component("rust", "Rust SDK", "sdk-rust/v", ("sdk-rust/v*",), ("sdk-rust",)),
    Component(
        "java",
        "Java SDK",
        "sdk-java/v",
        ("sdk-java/v*",),
        ("sdk-java", *SHARED_RUST_PATHS, "sdk-rust/crates/dex-blob-cache-jni"),
    ),
    Component(
        "python",
        "Python SDK",
        "sdk-python/v",
        ("sdk-python/v*",),
        ("sdk-python", *SHARED_RUST_PATHS, "sdk-rust/crates/dex-blob-cache-python"),
    ),
    Component(
        "typescript",
        "TypeScript SDK",
        "sdk-typescript/v",
        ("sdk-typescript/v*",),
        ("sdk-typescript", *SHARED_RUST_PATHS, "sdk-rust/crates/dex-blob-cache-node"),
    ),
    Component(
        "web",
        "Dex Web",
        "web/v",
        ("web/v*",),
        (
            "web",
            "sdk-go",
            "protos",
            ".dockerignore",
            ".github/workflows/docker-image-web-release.yml",
        ),
    ),
    Component(
        "cli",
        "CLI",
        "cli-v",
        ("cli-v*",),
        ("cli", "web", "sdk-go", "protos", "go.work"),
    ),
    Component(
        "agents",
        "Agents packages",
        "agents/v",
        ("agents/v*",),
        (
            "agents",
            ".github/workflows/agents-python-publish.yml",
            ".github/workflows/agents-typescript-publish.yml",
        ),
    ),
)


def git(*arguments: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ("git", *arguments),
        check=check,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )


def latest_reachable_tag(component: Component) -> str | None:
    result = git(
        "for-each-ref",
        "--merged=HEAD",
        "--format=%(refname:short)",
        *(f"refs/tags/{pattern}" for pattern in component.baseline_patterns),
    )
    tags = [line for line in result.stdout.splitlines() if line]
    versioned_tags = [
        (version_key, tag)
        for tag in tags
        if (version_key := tag_version_key(component, tag)) is not None
    ]
    return max(versioned_tags)[1] if versioned_tags else None


def tag_version_key(component: Component, tag: str) -> tuple[object, ...] | None:
    version = next(
        tag[len(pattern) - 1 :]
        for pattern in component.baseline_patterns
        if tag.startswith(pattern[:-1])
    )
    if not VERSION_PATTERN.fullmatch(version):
        return None
    core, separator, prerelease = version.partition("-")
    major, minor, patch = (int(part) for part in core.split("."))
    if not separator:
        return major, minor, patch, 1
    identifiers = tuple(
        (0, int(identifier)) if identifier.isdigit() else (1, identifier)
        for identifier in prerelease.split(".")
    )
    return major, minor, patch, 0, identifiers


def has_changes(component: Component, baseline: str | None) -> bool:
    if baseline is None:
        result = git("ls-files", "--", *component.paths)
    else:
        result = git("diff", "--name-only", f"{baseline}..HEAD", "--", *component.paths)
    return bool(result.stdout.splitlines())


def tag_exists(tag: str) -> bool:
    result = git("show-ref", "--verify", "--quiet", f"refs/tags/{tag}", check=False)
    return result.returncode == 0


def release_notes_errors(component: Component, version: str) -> list[str]:
    """Returns why the component's release notes cannot be built from its own change log, or no errors."""
    path = component_changelog_path(component.key)
    try:
        build_component_release_notes(component.key, Path(path).read_text(encoding="utf-8"), version, None, "superdurable/dex")
    except (OSError, ReleaseNotesError) as error:
        return [f"{component.name} release notes from {path}: {error}"]
    return []


def append_summary(lines: list[str]) -> None:
    summary_path = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary_path:
        with Path(summary_path).open("a", encoding="utf-8") as summary:
            summary.write("\n".join(lines) + "\n")


def write_outputs(output_path: Path, versions: dict[str, str], allow_unchanged: bool) -> int:
    selections: list[tuple[Component, str | None, bool, str, str]] = []
    collisions: list[str] = []
    preflight_errors: list[str] = []
    agents_go_tags: list[str] = []
    if not versions:
        print("Select at least one component version", file=sys.stderr)
        return 1

    for component in COMPONENTS:
        component_version = versions.get(component.key)
        selected = component_version is not None
        baseline = latest_reachable_tag(component) if selected else None
        target = f"{component.tag_prefix}{component_version}" if selected else ""
        selections.append((component, baseline, selected, target, "skip (no version)"))
        if selected and tag_exists(target):
            collisions.append(target)
        if selected:
            if not has_changes(component, baseline) and not allow_unchanged:
                preflight_errors.append(
                    f"{component.name} has no changes since {baseline}; pass --allow-unchanged to release it"
                )
            # The release snapshots the reference from main, so its change-log entry must already be there.
            preflight_errors.extend(release_readiness_errors(TreeReader(Path.cwd(), None), component.key, component_version))
            preflight_errors.extend(release_notes_errors(component, component_version))
        if selected and component.key == "agents":
            # Go builds each agents Go module from its own tag on the release commit.
            agents_go_tags = go_module_tags(Path.cwd(), component_version)
            collisions.extend(tag for tag in agents_go_tags if tag_exists(tag))
            preflight_errors.extend(
                f"Agents Go modules: {error}" for error in go_module_errors(Path.cwd(), component_version)
            )

    summary = ["### Component release plan", "", f"Allow unchanged: {'yes' if allow_unchanged else 'no'}", "", "| Component | Baseline | Result |", "|---|---|---|"]
    output_lines = [f"agents_version={versions.get('agents', '')}", f"agents_go_tags={' '.join(agents_go_tags)}"]
    for component, baseline, selected, target, skip_reason in selections:
        output_lines.extend(
            (
                f"{component.key}={'true' if selected else 'false'}",
                f"{component.key}_version={versions.get(component.key, '')}",
                f"{component.key}_tag={target}",
                f"{component.key}_baseline={baseline or ''}",
            )
        )
        baseline_text = baseline or "none (first release)"
        result_text = f"release {target}" if selected else skip_reason
        if component.key == "agents" and agents_go_tags:
            result_text += " and " + ", ".join(agents_go_tags)
        summary.append(f"| {component.name} | {baseline_text} | {result_text} |")

    append_summary(summary)
    if collisions:
        print("Target release tags already exist:", file=sys.stderr)
        for collision in collisions:
            print(f"- {collision}", file=sys.stderr)
        return 1
    if preflight_errors:
        for preflight_error in preflight_errors:
            print(preflight_error, file=sys.stderr)
        return 1

    output_path.write_text("\n".join(output_lines) + "\n", encoding="utf-8")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, required=True)
    for component in COMPONENTS:
        parser.add_argument(f"--{component.key}-version", help=f"{component.name} version; omit to skip")
    parser.add_argument("--allow-unchanged", action="store_true")
    arguments = parser.parse_args()
    versions = {
        component.key: version
        for component in COMPONENTS
        if (version := getattr(arguments, f"{component.key}_version"))
    }
    for component, version in versions.items():
        if not VERSION_PATTERN.fullmatch(version):
            print(f"Invalid semantic version for {component}: {version}", file=sys.stderr)
            return 1

    return write_outputs(arguments.output, versions, arguments.allow_unchanged)


if __name__ == "__main__":
    raise SystemExit(main())
