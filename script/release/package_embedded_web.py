# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import argparse
import base64
import gzip
import hashlib
import io
import json
import os
import re
import subprocess
import tarfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
WEB_EXPORTS = {
    ".": "./app/v2/V2Canvas.tsx",
    "./V2Canvas": "./app/v2/V2Canvas.tsx",
    "./RunWorkspace": "./app/v2/RunWorkspace.tsx",
    "./WorkQueueWorkspace": "./app/v2/work-queue/WorkQueueWorkspace.tsx",
    "./WebCatalogProvider": "./app/v2/WebCatalogProvider.tsx",
    "./PreferencesProvider": "./app/providers.tsx",
    "./StartFlowDialog": "./app/v2/start/StartFlowDialog.tsx",
    "./types": "./lib/types.ts",
    "./css": "./app/v2/css/v2.css",
}
IMPORT_PATTERN = re.compile(r"((?:from\s*|import\s*\(\s*|import\s*|@import\s*)['\"])([^'\"]+)(['\"])")
VERSION_PATTERN = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")


def build_package(component: str, version: str, output: Path) -> Path:
    if not VERSION_PATTERN.fullmatch(version):
        raise ValueError("Version must be a stable semantic version")
    source_commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    files: dict[str, bytes] = {}
    if component == "dex-web-v2":
        manifest = {
            "name": "@superdurable/dex-web-v2", "version": version, "type": "module",
            "license": "SEE LICENSE IN LICENSE", "exports": WEB_EXPORTS,
            "peerDependencies": {
                "@superdurable/flow-definition-renderer": "0.2.0",
                "@xyflow/react": "^12.8.4", "dagre": "^0.8.5", "react": "^19.0.0",
                "react-dom": "^19.0.0", "react-router-dom": "^7.0.0",
            },
        }
        collect_sources(ROOT / "web", [value.removeprefix("./") for value in WEB_EXPORTS.values()], files)
    else:
        source_root = ROOT / "packages/flow-definition-renderer"
        manifest = json.loads((source_root / "package.json").read_text())
        manifest.update(version=version, license="SEE LICENSE IN LICENSE")
        manifest.pop("private", None)
        manifest.pop("devDependencies", None)
        collect_sources(source_root, ["src/index.ts"], files)
    files["package.json"] = (json.dumps(manifest, indent=2) + "\n").encode()
    files["LICENSE"] = (ROOT / "LICENSE").read_bytes()
    files["provenance.json"] = (json.dumps({"sourceRepository": "https://github.com/superdurable/dex", "sourceCommit": source_commit, "component": component, "version": version}, sort_keys=True) + "\n").encode()
    package_bytes = reproducible_archive(files)
    output.mkdir(parents=True, exist_ok=True)
    archive = output / f"superdurable-{component}-{version}.tgz"
    archive.write_bytes(package_bytes)
    receipt = {
        "component": component, "version": version, "sourceCommit": source_commit,
        "filename": archive.name, "sha256": hashlib.sha256(package_bytes).hexdigest(),
        "integrity": "sha512-" + base64.b64encode(hashlib.sha512(package_bytes).digest()).decode(),
        "fileCount": len(files),
    }
    archive.with_suffix(".provenance.json").write_text(json.dumps(receipt, indent=2) + "\n")
    print(json.dumps(receipt))
    return archive


def collect_sources(source_root: Path, entries: list[str], files: dict[str, bytes]) -> None:
    pending = [resolve_source(source_root, entry) for entry in entries]
    while pending:
        source = pending.pop()
        relative_path = source.relative_to(source_root).as_posix()
        if relative_path in files:
            continue
        text = source.read_text()

        def resolve_import(match: re.Match[str]) -> str:
            specifier = match[2]
            if not specifier.startswith(("@/", ".")):
                return match[0]
            target = (source_root / specifier[2:]) if specifier.startswith("@/") else (source.parent / specifier)
            target = target.resolve()
            target.relative_to(source_root)
            pending.append(resolve_source(source_root, target.relative_to(source_root).as_posix()))
            if not specifier.startswith("@/"):
                return match[0]
            relative_import = Path(os.path.relpath(target, source.parent)).as_posix()
            if not relative_import.startswith("."):
                relative_import = "./" + relative_import
            return match[1] + relative_import + match[3]

        files[relative_path] = IMPORT_PATTERN.sub(resolve_import, text).encode()


def resolve_source(source_root: Path, relative_path: str) -> Path:
    target = source_root / relative_path
    for candidate in (target, target.with_suffix(".ts"), target.with_suffix(".tsx"), target / "index.ts", target / "index.tsx"):
        if candidate.is_file():
            return candidate
    raise ValueError(f"Unresolved package source: {relative_path}")


def reproducible_archive(files: dict[str, bytes]) -> bytes:
    buffer = io.BytesIO()
    with gzip.GzipFile(fileobj=buffer, mode="wb", filename="", mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as archive:
            for name, contents in sorted(files.items()):
                entry = tarfile.TarInfo("package/" + name)
                entry.size = len(contents)
                entry.mode = 0o644
                archive.addfile(entry, io.BytesIO(contents))
    return buffer.getvalue()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Package reproducible official Dex embedding sources")
    parser.add_argument("component", choices=("dex-web-v2", "flow-definition-renderer"))
    parser.add_argument("version")
    parser.add_argument("--output", type=Path, required=True)
    arguments = parser.parse_args()
    build_package(arguments.component, arguments.version, arguments.output)
