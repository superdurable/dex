# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor
from functools import lru_cache
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from typing import Any


ROOT = Path(os.environ.get("SUPER_DURABLE_REPO_ROOT", Path(__file__).resolve().parents[2])).resolve()
HEADERS_DIR = Path(
    os.environ.get("SUPER_DURABLE_HEADERS_DIR", ROOT / "script" / "licenseheaders")
).resolve()
POLICY_PATH = HEADERS_DIR / "policy.json"
MANIFEST_PATH = HEADERS_DIR / "legacy-manifest.json"
REPORT_PATH = HEADERS_DIR / "migration-report.json"
SUPPORTED_SUFFIXES = {
    ".css",
    ".go",
    ".html",
    ".java",
    ".proto",
    ".py",
    ".rs",
    ".ts",
    ".tsx",
    ".yaml",
    ".yml",
}
GENERATED_PARTS = {"gen", "dexpb", "vendor", "target", "node_modules", "__pycache__"}
HEADER_MARKERS = (
    "copyright",
    "licensed under",
    "permission is hereby granted",
    "spdx-license-identifier",
    "legacy materials",
    "modifications after the legacy cutoff",
    "dual-licensed",
)


def main() -> int:
    args = parse_args()
    policy = read_json(POLICY_PATH)
    selected = selected_paths(args.paths)
    migrator = HeaderMigrator(policy, selected)
    report, manifest = migrator.analyze()
    persisted_report = merge_report(report, selected)
    persisted_manifest = merge_manifest(manifest, selected)
    write_json(REPORT_PATH, persisted_report)
    if args.report:
        print(json.dumps(report["summary"], indent=2, sort_keys=True))
    if args.apply:
        migrator.apply(report)
        write_json(MANIFEST_PATH, persisted_manifest)
        print(json.dumps(report["summary"], indent=2, sort_keys=True))
    if report["summary"]["manual-review"]:
        print(f"manual review required; see {REPORT_PATH.relative_to(ROOT)}", file=sys.stderr)
        return 1
    return 0


def merge_report(report: dict[str, Any], selected: set[str] | None) -> dict[str, Any]:
    if selected is None or not REPORT_PATH.exists():
        return report
    existing = read_json(REPORT_PATH)
    entries_by_path = {str(entry["path"]): entry for entry in existing.get("entries", [])}
    entries_by_path.update({str(entry["path"]): entry for entry in report["entries"]})
    entries = sorted(entries_by_path.values(), key=lambda entry: str(entry["path"]))
    merged = dict(report)
    merged["entries"] = entries
    merged["summary"] = classification_counts(entries)
    return merged


def merge_manifest(manifest: dict[str, Any], selected: set[str] | None) -> dict[str, Any]:
    if selected is None or not MANIFEST_PATH.exists():
        return manifest
    existing = read_json(MANIFEST_PATH)
    entries = dict(existing.get("entries", {}))
    entries.update(manifest["entries"])
    return {"version": manifest["version"], "cutoff": manifest["cutoff"], "entries": dict(sorted(entries.items()))}


def classification_counts(entries: list[dict[str, Any]]) -> dict[str, int]:
    counts: dict[str, int] = {}
    for entry in entries:
        classification = str(entry["classification"])
        counts[classification] = counts.get(classification, 0) + 1
    for classification in ("excluded", "legacy-only", "mixed", "new", "manual-review"):
        counts.setdefault(classification, 0)
    return counts


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Migrate source headers using the legacy cutoff")
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--report", action="store_true", help="analyze and write the report")
    mode.add_argument("--apply", action="store_true", help="apply safe changes and write the manifest")
    parser.add_argument("--paths", nargs="*", help="limit analysis to repository-relative paths")
    return parser.parse_args()


def selected_paths(values: list[str] | None) -> set[str] | None:
    if not values:
        return None
    return {normalize_path(value) for value in values}


class HeaderMigrator:
    def __init__(self, policy: dict[str, Any], selected: set[str] | None) -> None:
        self.policy = policy
        self.selected = selected
        self.cutoff = str(policy["cutoff"])
        self.contamination_commit = str(policy["contamination_commit"])
        self.contamination_parent = git("rev-parse", f"{self.contamination_commit}^").strip()
        self.dirty_paths = get_dirty_paths()
        self.repair_from_head_paths = set(policy.get("repair_from_head_paths", []))
        self.rename_sources = get_rename_sources(self.cutoff)
        self.boundary_source_map = self.find_boundary_source_map()
        self.contaminated_paths = self.find_contaminated_paths()
        self.templates = {
            "new": read_text(HEADERS_DIR / "super-durable-1.0.txt"),
            "mixed": read_text(HEADERS_DIR / "mixed.txt"),
            "legacy": read_text(HEADERS_DIR / "legacy-reference.txt"),
        }

    def analyze(self) -> tuple[dict[str, Any], dict[str, Any]]:
        entries: list[dict[str, Any]] = []
        manifest_entries: dict[str, Any] = {}
        for current_path in tracked_source_paths():
            if self.selected is not None and not path_selected(current_path, self.selected):
                continue
            entry = self.classify(current_path)
            entries.append(entry)
            if entry["classification"] not in {"excluded", "manual-review"}:
                manifest_entries[current_path] = manifest_entry(entry)
        counts = classification_counts(entries)
        report = {
            "version": 1,
            "cutoff": self.cutoff,
            "contamination_commit": self.contamination_commit,
            "summary": counts,
            "entries": entries,
        }
        manifest = {
            "version": 1,
            "cutoff": self.cutoff,
            "entries": dict(sorted(manifest_entries.items())),
        }
        return report, manifest

    def classify(self, current_path: str) -> dict[str, Any]:
        if is_excluded(current_path, self.policy) or is_generated(current_path):
            return basic_entry(current_path, "excluded", "excluded by policy")
        if not is_included(current_path, self.policy):
            return basic_entry(current_path, "excluded", "outside managed prefixes")
        repair_from_head = current_path in self.dirty_paths and current_path in self.repair_from_head_paths
        if current_path in self.dirty_paths and not repair_from_head:
            return basic_entry(current_path, "manual-review", "path was dirty before migration")

        actual_current = (ROOT / current_path).read_bytes()
        current = git_bytes("show", f"HEAD:{current_path}") if repair_from_head else actual_current
        if is_forced_new(current_path, self.policy):
            return self.build_entry(
                current_path,
                "new",
                "directory is declared wholly new",
                current,
                actual_current=actual_current,
                repair_from_head=repair_from_head,
            )
        direct = git_object_exists(self.cutoff, current_path)
        rename_source = self.rename_sources.get(current_path)
        source_path = current_path if direct else rename_source[0] if rename_source else None
        source_kind = "direct" if direct else rename_source[1] if rename_source else ""

        if source_path is None:
            blame_sources = self.boundary_source_map.get(current_path, set())
            if len(blame_sources) == 1:
                source_path = next(iter(blame_sources))
                source_kind = "blame-copy"
            elif len(blame_sources) > 1:
                if current_path in set(self.policy["reviewed_mixed_paths"]):
                    return self.build_multi_source_entry(
                        current_path,
                        current,
                        actual_current,
                        blame_sources,
                        repair_from_head,
                    )
                return basic_entry(
                    current_path,
                    "manual-review",
                    "multiple legacy source files detected",
                    legacy_paths=sorted(blame_sources),
                )

        if source_path is None:
            return self.build_entry(
                current_path,
                "new",
                "created after cutoff",
                current,
                actual_current=actual_current,
                repair_from_head=repair_from_head,
            )
        if not git_object_exists(self.cutoff, source_path):
            return basic_entry(
                current_path,
                "manual-review",
                "legacy source is not present at cutoff",
                legacy_paths=[source_path],
            )

        baseline = git_bytes("show", f"{self.cutoff}:{source_path}")
        baseline_hash = content_hash(normalized_body(baseline, Path(source_path).suffix))
        current_hash = content_hash(normalized_body(current, Path(current_path).suffix))
        classification = "legacy-only" if source_kind in {"direct", "rename"} and current_hash == baseline_hash else "mixed"
        reason = "content matches cutoff" if classification == "legacy-only" else f"legacy content changed or copied ({source_kind})"
        return self.build_entry(
            current_path,
            classification,
            reason,
            current,
            source_path=source_path,
            baseline_hash=baseline_hash,
            actual_current=actual_current,
            repair_from_head=repair_from_head,
        )

    def build_multi_source_entry(
        self,
        current_path: str,
        current: bytes,
        actual_current: bytes,
        source_paths: set[str],
        repair_from_head: bool,
    ) -> dict[str, Any]:
        legacy_licenses = sorted(
            {
                license_name
                for source_path in source_paths
                for license_name in licenses_for_path(source_path, self.policy)
            }
        )
        new_content = render_migrated_content(
            current,
            Path(current_path).suffix,
            "mixed",
            "",
            self.templates,
        )
        entry = basic_entry(
            current_path,
            "mixed",
            "multiple legacy sources reviewed and retained",
            legacy_paths=sorted(source_paths),
        )
        entry.update(
            {
                "legacy_path": None,
                "legacy_licenses": legacy_licenses,
                "baseline_hash": None,
                "before_header_hash": content_hash(extract_header(current, Path(current_path).suffix).encode()),
                "after_header_hash": content_hash(extract_header(new_content, Path(current_path).suffix).encode()),
                "changed": new_content != actual_current,
                "repair_from_head": repair_from_head,
            }
        )
        return entry

    def find_boundary_source_map(self) -> dict[str, set[str]]:
        candidates = [
            path
            for path in tracked_source_paths()
            if is_included(path, self.policy)
            and not is_excluded(path, self.policy)
            and not is_generated(path)
            and (self.selected is None or path_selected(path, self.selected))
            and not is_forced_new(path, self.policy)
            and not git_object_exists(self.cutoff, path)
            and path not in self.rename_sources
        ]
        with ThreadPoolExecutor(max_workers=12) as executor:
            sources = executor.map(lambda path: boundary_sources(self.cutoff, path), candidates)
            return dict(zip(candidates, sources))

    def build_entry(
        self,
        current_path: str,
        classification: str,
        reason: str,
        current: bytes,
        source_path: str | None = None,
        baseline_hash: str | None = None,
        actual_current: bytes | None = None,
        repair_from_head: bool = False,
    ) -> dict[str, Any]:
        original_header = ""
        legacy_licenses: list[str] = []
        if source_path is not None:
            original_header = self.original_legacy_header(source_path)
            legacy_licenses = licenses_for_path(source_path, self.policy)
        new_content = render_migrated_content(
            current,
            Path(current_path).suffix,
            classification,
            original_header,
            self.templates,
        )
        entry = basic_entry(current_path, classification, reason)
        if actual_current is None:
            actual_current = current
        entry.update(
            {
                "legacy_path": source_path,
                "legacy_licenses": legacy_licenses,
                "baseline_hash": baseline_hash,
                "before_header_hash": content_hash(extract_header(current, Path(current_path).suffix).encode()),
                "after_header_hash": content_hash(extract_header(new_content, Path(current_path).suffix).encode()),
                "changed": new_content != actual_current,
                "repair_from_head": repair_from_head,
            }
        )
        return entry

    def original_legacy_header(self, source_path: str) -> str:
        if source_path in self.contaminated_paths and git_object_exists(self.contamination_parent, source_path):
            original = git_bytes("show", f"{self.contamination_parent}:{source_path}")
            return extract_header(original, Path(source_path).suffix)
        baseline = git_bytes("show", f"{self.cutoff}:{source_path}")
        return extract_header(baseline, Path(source_path).suffix)

    def find_contaminated_paths(self) -> set[str]:
        contaminated: set[str] = set()
        for changed_path in changed_paths(self.contamination_parent, self.contamination_commit):
            if not git_object_exists(self.contamination_parent, changed_path):
                continue
            if not git_object_exists(self.contamination_commit, changed_path):
                continue
            before = git_bytes("show", f"{self.contamination_parent}:{changed_path}")
            after = git_bytes("show", f"{self.contamination_commit}:{changed_path}")
            if normalized_body(before, Path(changed_path).suffix) == normalized_body(after, Path(changed_path).suffix):
                if extract_header(before, Path(changed_path).suffix) != extract_header(after, Path(changed_path).suffix):
                    contaminated.add(changed_path)
        return contaminated

    def apply(self, report: dict[str, Any]) -> None:
        unresolved = [entry for entry in report["entries"] if entry["classification"] == "manual-review"]
        if unresolved:
            raise RuntimeError("refusing to apply while manual-review entries remain")
        for entry in report["entries"]:
            if not entry.get("changed"):
                continue
            current_path = str(entry["path"])
            path = ROOT / current_path
            current = (
                git_bytes("show", f"HEAD:{current_path}")
                if entry.get("repair_from_head")
                else path.read_bytes()
            )
            original_header = ""
            source_path = entry.get("legacy_path")
            if source_path:
                original_header = self.original_legacy_header(str(source_path))
            new_content = render_migrated_content(
                current,
                path.suffix,
                str(entry["classification"]),
                original_header,
                self.templates,
            )
            atomic_write(path, new_content)


def basic_entry(path: str, classification: str, reason: str, **extra: Any) -> dict[str, Any]:
    entry: dict[str, Any] = {"path": path, "classification": classification, "reason": reason}
    entry.update(extra)
    return entry


def manifest_entry(entry: dict[str, Any]) -> dict[str, Any]:
    return {
        "classification": entry["classification"],
        "legacy_path": entry.get("legacy_path"),
        "legacy_paths": entry.get("legacy_paths", []),
        "legacy_licenses": entry.get("legacy_licenses", []),
        "baseline_hash": entry.get("baseline_hash"),
    }


def tracked_source_paths() -> list[str]:
    paths = git_bytes("ls-files", "-z").decode().split("\0")
    return sorted(path for path in paths if path and Path(path).suffix.lower() in SUPPORTED_SUFFIXES)


def get_dirty_paths() -> set[str]:
    commands = (
        ("diff", "--name-only", "-z"),
        ("diff", "--cached", "--name-only", "-z"),
        ("ls-files", "--others", "--exclude-standard", "-z"),
    )
    dirty: set[str] = set()
    for command in commands:
        dirty.update(path for path in git_bytes(*command).decode().split("\0") if path)
    substantive: set[str] = set()
    for path in dirty:
        current_path = ROOT / path
        if not current_path.is_file() or Path(path).suffix.lower() not in SUPPORTED_SUFFIXES:
            substantive.add(path)
            continue
        if not git_object_exists("HEAD", path):
            substantive.add(path)
            continue
        head = git_bytes("show", f"HEAD:{path}")
        current = current_path.read_bytes()
        if normalized_body(head, current_path.suffix) != normalized_body(current, current_path.suffix):
            substantive.add(path)
    return substantive


def get_rename_sources(cutoff: str) -> dict[str, tuple[str, str]]:
    raw = git_bytes(
        "diff",
        "--name-status",
        "-z",
        "-M20%",
        "-C20%",
        "--find-copies-harder",
        cutoff,
        "HEAD",
    ).decode()
    fields = raw.split("\0")
    result: dict[str, tuple[str, str]] = {}
    index = 0
    while index < len(fields) and fields[index]:
        status = fields[index]
        index += 1
        if index >= len(fields):
            break
        source = fields[index]
        index += 1
        if status.startswith(("R", "C")) and index < len(fields):
            destination = fields[index]
            index += 1
            source_kind = "rename" if status.startswith("R") else "copy"
            result[destination] = (source, source_kind)
    return result


def boundary_sources(cutoff: str, path: str) -> set[str]:
    head_content = git_bytes("show", f"HEAD:{path}")
    header = extract_header(head_content, Path(path).suffix)
    header_lines = len(header.splitlines())
    try:
        result = run_git(
            "blame",
            "-C",
            "-C",
            "-C",
            "--line-porcelain",
            f"{cutoff}..HEAD",
            "--",
            path,
            check=False,
            timeout=30,
        )
    except subprocess.TimeoutExpired:
        return {"__TIMEOUT__"}
    if result.returncode != 0:
        return set()
    sources: set[str] = set()
    boundary = False
    current_line = 0
    for line in result.stdout.decode(errors="replace").splitlines():
        fields = line.split()
        if len(fields) >= 3 and len(fields[0]) == 40:
            try:
                current_line = int(fields[2])
            except ValueError:
                current_line = 0
        elif line == "boundary":
            boundary = True
        elif boundary and line.startswith("filename "):
            if current_line > header_lines:
                sources.add(line.removeprefix("filename "))
            boundary = False
    return sources


def changed_paths(before: str, after: str) -> set[str]:
    raw = git_bytes("diff", "--name-only", "-z", before, after).decode()
    return {path for path in raw.split("\0") if path}


def licenses_for_path(path: str, policy: dict[str, Any]) -> list[str]:
    best_prefix = ""
    licenses: list[str] = []
    for prefix, values in policy["legacy_licenses"].items():
        if has_prefix(path, prefix) and len(prefix) > len(best_prefix):
            best_prefix = prefix
            licenses = list(values)
    return licenses


def is_excluded(path: str, policy: dict[str, Any]) -> bool:
    return any(has_prefix(path, prefix) for prefix in policy["excluded_prefixes"])


def is_included(path: str, policy: dict[str, Any]) -> bool:
    return any(has_prefix(path, prefix) for prefix in policy["included_prefixes"])


def is_forced_new(path: str, policy: dict[str, Any]) -> bool:
    return any(has_prefix(path, prefix) for prefix in policy["forced_new_prefixes"])


def is_generated(path: str) -> bool:
    parts = set(Path(path).parts)
    name = Path(path).name
    return bool(parts & GENERATED_PARTS) or any(
        (
            ".gen." in name,
            name.endswith("_pb.go"),
            name.endswith(".pb.go"),
            name.endswith("_pb2.py"),
            name.endswith("_pb2_grpc.py"),
            name.endswith("_pb2.pyi"),
        )
    )


def render_migrated_content(
    current: bytes,
    suffix: str,
    classification: str,
    original_header: str,
    templates: dict[str, str],
) -> bytes:
    shebang, body = split_shebang(current.decode())
    body_without_header = strip_managed_blocks(strip_header(body, suffix), suffix)
    if classification == "new":
        plain_headers = [templates["new"]]
        preserved = ""
    elif classification == "mixed":
        plain_headers = [templates["mixed"]]
        preserved = original_header
        if not preserved:
            plain_headers.insert(0, templates["legacy"])
    elif classification == "legacy-only":
        plain_headers = [] if original_header else [templates["legacy"]]
        preserved = original_header
    else:
        return current
    rendered = preserved.rstrip("\n")
    if rendered:
        rendered += "\n\n"
    rendered += "\n\n".join(format_header(header, suffix).rstrip("\n") for header in plain_headers)
    body_without_header = body_without_header.lstrip("\n")
    if rendered:
        rendered += "\n\n" if body_without_header else "\n"
    return (shebang + rendered + body_without_header).encode()


def normalized_body(content: bytes, suffix: str) -> bytes:
    _, body = split_shebang(content.decode(errors="surrogateescape"))
    normalized = strip_managed_blocks(strip_header(body, suffix), suffix)
    return normalized.lstrip("\n").encode(errors="surrogateescape")


def extract_header(content: bytes, suffix: str) -> str:
    _, body = split_shebang(content.decode(errors="surrogateescape"))
    header, _ = split_header(body, suffix)
    return header.rstrip("\n")


def strip_header(content: str, suffix: str) -> str:
    _, body = split_header(content, suffix)
    return body


def strip_managed_blocks(content: str, suffix: str) -> str:
    result = content
    for header in managed_formatted_headers(suffix):
        result = result.replace("\n\n" + header + "\n\n", "\n")
        result = result.replace(header, "")
    return reflow_blank_lines(result)


@lru_cache(maxsize=None)
def managed_formatted_headers(suffix: str) -> tuple[str, ...]:
    names = ("super-durable-1.0.txt", "mixed.txt", "legacy-reference.txt")
    return tuple(format_header(read_text(HEADERS_DIR / name), suffix) for name in names)


def reflow_blank_lines(content: str) -> str:
    while "\n\n\n" in content:
        content = content.replace("\n\n\n", "\n\n")
    return content


def split_header(content: str, suffix: str) -> tuple[str, str]:
    if suffix in {".css", ".java"}:
        offset = 0
        headers: list[str] = []
        while True:
            while offset < len(content) and content[offset] in " \t\r\n":
                offset += 1
            if not content.startswith("/*", offset):
                break
            end = content.find("*/", offset)
            if end < 0:
                break
            candidate = content[offset : end + 2]
            if not header_candidate(candidate):
                break
            headers.append(candidate)
            offset = end + 2
        if headers:
            return "\n\n".join(headers), content[offset:]
        return "", content
    if suffix == ".html":
        offset = 0
        headers = []
        while True:
            while offset < len(content) and content[offset] in " \t\r\n":
                offset += 1
            if not content.startswith("<!--", offset):
                break
            end = content.find("-->", offset)
            if end < 0:
                break
            candidate = content[offset : end + 3]
            if not header_candidate(candidate):
                break
            headers.append(candidate)
            offset = end + 3
        if headers:
            return "\n\n".join(headers), content[offset:]
        return "", content
    prefix = "#" if suffix in {".py", ".yaml", ".yml"} else "//"
    lines = content.splitlines(keepends=True)
    index = 0
    headers: list[str] = []
    while True:
        while index < len(lines) and not lines[index].strip():
            index += 1
        start = index
        candidate: list[str] = []
        while index < len(lines) and lines[index].lstrip().startswith(prefix):
            candidate.append(lines[index])
            index += 1
        text = "".join(candidate).rstrip("\n")
        if not text or not header_candidate(text):
            if headers:
                return "\n\n".join(headers), "".join(lines[start:])
            return "", content
        headers.append(text)
    if headers:
        return "\n\n".join(headers), "".join(lines[index:])
    return "", content


def header_candidate(text: str) -> bool:
    lowered = text.lower()
    return any(marker in lowered for marker in HEADER_MARKERS)


def split_shebang(content: str) -> tuple[str, str]:
    if not content.startswith("#!"):
        return "", content
    newline = content.find("\n")
    if newline < 0:
        return content + "\n", ""
    return content[: newline + 1], content[newline + 1 :]


def format_header(plain: str, suffix: str) -> str:
    lines = plain.rstrip("\n").split("\n")
    if suffix in {".css", ".java"}:
        formatted = ["/*"]
        formatted.extend(" *" if not line else f" * {line}" for line in lines)
        formatted.append(" */")
        return "\n".join(formatted)
    if suffix == ".html":
        formatted = ["<!--"]
        formatted.extend("" if not line else line for line in lines)
        formatted.append("-->")
        return "\n".join(formatted)
    prefix = "#" if suffix in {".py", ".yaml", ".yml"} else "//"
    return "\n".join(prefix if not line else f"{prefix} {line}" for line in lines)


def atomic_write(path: Path, content: bytes) -> None:
    mode = path.stat().st_mode
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as temporary:
        temporary.write(content)
        temporary_path = Path(temporary.name)
    os.chmod(temporary_path, mode)
    os.replace(temporary_path, path)


def path_selected(path: str, selected: set[str]) -> bool:
    return any(path == value or path.startswith(value.rstrip("/") + "/") for value in selected)


def has_prefix(path: str, prefix: str) -> bool:
    return path == prefix or path.startswith(prefix.rstrip("/") + "/")


def normalize_path(value: str) -> str:
    candidate = Path(value)
    if candidate.is_absolute():
        candidate = candidate.relative_to(ROOT)
    normalized = candidate.as_posix().strip("/")
    if normalized.startswith("../") or normalized == "..":
        raise ValueError(f"path escapes repository: {value}")
    return normalized


def content_hash(content: bytes) -> str:
    return hashlib.sha256(content).hexdigest()


def read_text(path: Path) -> str:
    return path.read_text().rstrip("\n")


def read_json(path: Path) -> dict[str, Any]:
    return json.loads(path.read_text())


def write_json(path: Path, value: dict[str, Any]) -> None:
    content = json.dumps(value, indent=2, sort_keys=True) + "\n"
    path.write_text(content)


def git_object_exists(commit: str, path: str) -> bool:
    return run_git("cat-file", "-e", f"{commit}:{path}", check=False).returncode == 0


def git(*args: str) -> str:
    return git_bytes(*args).decode(errors="replace")


def git_bytes(*args: str) -> bytes:
    return run_git(*args).stdout


def run_git(
    *args: str,
    check: bool = True,
    timeout: int | None = None,
) -> subprocess.CompletedProcess[bytes]:
    return subprocess.run(
        ["git", *args],
        cwd=ROOT,
        check=check,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=timeout,
    )


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, RuntimeError, subprocess.CalledProcessError, ValueError) as error:
        print(error, file=sys.stderr)
        raise SystemExit(1)
