# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path
from typing import Any


VERSION_PATTERN = re.compile(
    r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
    r"(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$"
)
COMMIT_PATTERN = re.compile(r"^[0-9a-f]{40}$")
DIGEST_PATTERN = re.compile(r"^sha256:[0-9a-f]{64}$")
CHECKSUM_PATTERN = re.compile(r"^[0-9a-f]{64}$")


@dataclass(frozen=True)
class Component:
    key: str
    tag_prefix: str

    def tag(self, version: str) -> str:
        return f"{self.tag_prefix}{version}"


COMPONENTS = (
    Component("server", "server/v"),
    Component("cli", "cli-v"),
    Component("sdkGo", "sdk-go/v"),
    Component("sdkJava", "sdk-java/v"),
    Component("sdkPython", "sdk-python/v"),
    Component("sdkRust", "sdk-rust/v"),
    Component("sdkTypeScript", "sdk-typescript/v"),
)

PROTOCOL_SOURCES = {
    "server": (
        "server/service/version.go",
        r"MinimumSupportedProtocolVersion\s+uint32\s*=\s*(\d+)",
        r"CurrentProtocolVersion\s+uint32\s*=\s*(\d+)",
    ),
    "cli": (
        "cli/internal/command/version.go",
        r"minimumSupportedServerProtocolVersion\s+uint32\s*=\s*(\d+)",
        r"maximumSupportedServerProtocolVersion\s+uint32\s*=\s*(\d+)",
    ),
    "sdkGo": (
        "sdk-go/dex/server_protocol.go",
        r"minimumSupportedServerProtocolVersion\s+uint32\s*=\s*(\d+)",
        r"maximumSupportedServerProtocolVersion\s+uint32\s*=\s*(\d+)",
    ),
    "sdkJava": (
        "sdk-java/src/main/java/io/superdurable/dex/ServerProtocolCompatibility.java",
        r"MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION\s*=\s*(\d+)",
        r"MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION\s*=\s*(\d+)",
    ),
    "sdkPython": (
        "sdk-python/dex/_server_protocol.py",
        r"MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION\s*=\s*(\d+)",
        r"MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION\s*=\s*(\d+)",
    ),
    "sdkRust": (
        "sdk-rust/crates/dex-sdk/src/server_protocol.rs",
        r"MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION:\s*u32\s*=\s*(\d+)",
        r"MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION:\s*u32\s*=\s*(\d+)",
    ),
    "sdkTypeScript": (
        "sdk-typescript/src/server-protocol.ts",
        r"minimumSupportedServerProtocolVersion:\s*number\s*=\s*(\d+)",
        r"maximumSupportedServerProtocolVersion:\s*number\s*=\s*(\d+)",
    ),
}

EXPECTED_CHECKSUM_NAMES = (
    "dexcli_v{version}_darwin_amd64.tar.gz",
    "dexcli_v{version}_darwin_arm64.tar.gz",
    "dexcli_v{version}_linux_amd64.tar.gz",
    "dexcli_v{version}_linux_arm64.tar.gz",
)


class ManifestError(RuntimeError):
    pass


def run(*command: str) -> str:
    try:
        result = subprocess.run(
            command,
            check=True,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
    except FileNotFoundError as error:
        raise ManifestError(f"required command is unavailable: {command[0]}") from error
    except subprocess.CalledProcessError as error:
        detail = error.stderr.strip() or error.stdout.strip()
        raise ManifestError(f"command failed: {' '.join(command)}: {detail}") from error
    return result.stdout


def git(*arguments: str) -> str:
    return run("git", *arguments).strip()


def load_declaration(path: Path, version: str) -> dict[str, Any]:
    try:
        declaration = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ManifestError(f"cannot read compatibility declaration: {path}: {error}") from error
    expected_keys = {
        "schemaVersion",
        "release",
        "rolloutOrder",
        "runningFlowsCompatibility",
        "persistenceCompatibility",
        "protocol",
    }
    if not isinstance(declaration, dict) or set(declaration) - {"componentVersions"} != expected_keys:
        raise ManifestError("compatibility declaration has unexpected fields")
    if declaration["schemaVersion"] != 1 or declaration["release"] != version:
        raise ManifestError("compatibility declaration version does not match the requested release")
    if declaration["rolloutOrder"] not in {"server-first"}:
        raise ManifestError("rolloutOrder must be server-first")
    if declaration["runningFlowsCompatibility"] not in {"compatible", "cancel-required"}:
        raise ManifestError("runningFlowsCompatibility is invalid")
    if declaration["persistenceCompatibility"] not in {
        "compatible",
        "migration-required",
        "reset-required",
    }:
        raise ManifestError("persistenceCompatibility is invalid")
    validate_protocol_declaration(declaration["protocol"])
    component_versions(declaration)
    return declaration


def component_versions(declaration: dict[str, Any]) -> dict[str, str]:
    versions = declaration.get("componentVersions", {
        component.key: declaration["release"] for component in COMPONENTS
    })
    if not isinstance(versions, dict) or set(versions) != {component.key for component in COMPONENTS}:
        raise ManifestError("componentVersions must name every component")
    if any(not isinstance(version, str) or not VERSION_PATTERN.fullmatch(version) for version in versions.values()):
        raise ManifestError("componentVersions contains an invalid version")
    if versions["server"] != declaration["release"]:
        raise ManifestError("Server component version must match the release")
    return versions


def validate_protocol_declaration(protocol: Any) -> None:
    if not isinstance(protocol, dict) or set(protocol) != {"server", "clients"}:
        raise ManifestError("protocol declaration has unexpected fields")
    clients = protocol["clients"]
    expected_clients = {component.key for component in COMPONENTS if component.key != "server"}
    if not isinstance(clients, dict) or set(clients) != expected_clients:
        raise ManifestError("protocol clients do not match released clients")
    for name, interval in (("server", protocol["server"]), *clients.items()):
        if not isinstance(interval, dict) or set(interval) != {"minimum", "maximum"}:
            raise ManifestError(f"protocol interval is invalid: {name}")
        minimum = interval["minimum"]
        maximum = interval["maximum"]
        if (
            not isinstance(minimum, int)
            or isinstance(minimum, bool)
            or not isinstance(maximum, int)
            or isinstance(maximum, bool)
            or minimum <= 0
            or maximum < minimum
        ):
            raise ManifestError(f"protocol interval is invalid: {name}")


def resolve_release_commits(versions: dict[str, str], verify_releases: bool) -> tuple[dict[str, str], dict[str, str]]:
    tags = {component.key: component.tag(versions[component.key]) for component in COMPONENTS}
    commits: dict[str, str] = {}
    for key, tag in tags.items():
        commit = git("rev-parse", f"refs/tags/{tag}^{{commit}}")
        if not COMMIT_PATTERN.fullmatch(commit):
            raise ManifestError(f"release tag did not resolve to a commit: {tag}")
        commits[key] = commit
        if verify_releases:
            metadata = json.loads(
                run(
                    "gh",
                    "release",
                    "view",
                    tag,
                    "--json",
                    "tagName,targetCommitish",
                )
            )
            if metadata.get("tagName") != tag or metadata.get("targetCommitish") != commit:
                raise ManifestError(f"GitHub release target does not match tag commit: {tag}")
    return commits, tags


def read_protocol_interval(commit: str, key: str) -> dict[str, int]:
    path, minimum_pattern, maximum_pattern = PROTOCOL_SOURCES[key]
    source = git("show", f"{commit}:{path}")
    minimum_match = re.search(minimum_pattern, source)
    maximum_match = re.search(maximum_pattern, source)
    if minimum_match is None or maximum_match is None:
        raise ManifestError(f"cannot read protocol interval from {path}")
    return {
        "minimum": int(minimum_match.group(1)),
        "maximum": int(maximum_match.group(1)),
    }


def verify_protocols(commits: dict[str, str], declared: dict[str, Any]) -> None:
    intervals = {"server": declared["server"], **declared["clients"]}
    for key, expected in intervals.items():
        actual = read_protocol_interval(commits[key], key)
        if actual != expected:
            raise ManifestError(
                f"protocol declaration does not match {key} source: expected {expected}, got {actual}"
            )
        server = declared["server"]
        if max(actual["minimum"], server["minimum"]) > min(actual["maximum"], server["maximum"]):
            raise ManifestError(f"{key} protocol does not overlap Server protocol")


def parse_cli_checksums(path: Path, version: str) -> dict[str, str]:
    checksums: dict[str, str] = {}
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError as error:
        raise ManifestError(f"cannot read CLI checksums: {path}: {error}") from error
    for line in lines:
        parts = line.split()
        if len(parts) != 2:
            raise ManifestError(f"invalid CLI checksum line: {line}")
        checksum, name = parts
        name = name.removeprefix("*").removeprefix("./")
        if not CHECKSUM_PATTERN.fullmatch(checksum) or name in checksums:
            raise ManifestError(f"invalid CLI checksum entry: {line}")
        checksums[name] = checksum
    expected_names = {name.format(version=version) for name in EXPECTED_CHECKSUM_NAMES}
    if set(checksums) != expected_names:
        raise ManifestError("CLI checksums do not contain exactly four supported archives")
    return dict(sorted(checksums.items()))


def download_cli_checksums(tag: str) -> tuple[tempfile.TemporaryDirectory[str], Path]:
    directory = tempfile.TemporaryDirectory(prefix="dex-compatibility-")
    run(
        "gh",
        "release",
        "download",
        tag,
        "--pattern",
        "checksums.txt",
        "--dir",
        directory.name,
    )
    return directory, Path(directory.name) / "checksums.txt"


def verify_cli_release_assets(tag: str, checksums: dict[str, str]) -> None:
    metadata = json.loads(run("gh", "release", "view", tag, "--json", "assets"))
    assets = {
        asset.get("name"): asset.get("digest")
        for asset in metadata.get("assets", [])
        if isinstance(asset, dict)
    }
    for name, checksum in checksums.items():
        if assets.get(name) != f"sha256:{checksum}":
            raise ManifestError(f"CLI checksum does not match release asset digest: {name}")


def resolve_server_digest(version: str) -> str:
    image = f"docker.io/superdurable/dex-server:v{version}"
    output = run("docker", "buildx", "imagetools", "inspect", image)
    match = re.search(r"^Digest:\s+(sha256:[0-9a-f]{64})$", output, re.MULTILINE)
    if match is None:
        raise ManifestError(f"cannot resolve immutable image digest: {image}")
    return match.group(1)


def require_digest(value: str) -> str:
    if not DIGEST_PATTERN.fullmatch(value):
        raise ManifestError("Server image digest must be sha256:<64 lowercase hex characters>")
    return value


def build_manifest(
    version: str,
    declaration_path: Path,
    server_digest: str,
    cli_checksums_path: Path,
    verify_releases: bool,
) -> dict[str, Any]:
    if not VERSION_PATTERN.fullmatch(version):
        raise ManifestError(f"invalid semantic version: {version}")
    declaration = load_declaration(declaration_path, version)
    versions = component_versions(declaration)
    commits, tags = resolve_release_commits(versions, verify_releases)
    verify_protocols(commits, declaration["protocol"])
    checksums = parse_cli_checksums(cli_checksums_path, versions["cli"])
    if verify_releases:
        verify_cli_release_assets(tags["cli"], checksums)
    components: dict[str, Any] = {}
    for component in COMPONENTS:
        value: dict[str, Any] = {"version": versions[component.key], "tag": tags[component.key]}
        if component.key == "server":
            value["image"] = (
                "docker.io/superdurable/dex-server@" + require_digest(server_digest)
            )
        elif component.key == "cli":
            value["checksums"] = checksums
        components[component.key] = value
    return {
        **{key: value for key, value in declaration.items() if key != "componentVersions"},
        "sourceCommit": commits["server"],
        "components": components,
    }


def write_manifest(path: Path, manifest: dict[str, Any]) -> str:
    encoded = (json.dumps(manifest, indent=2, sort_keys=True) + "\n").encode("utf-8")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(encoded)
    return hashlib.sha256(encoded).hexdigest()


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version")
    parser.add_argument("--declaration", type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--server-image-digest")
    parser.add_argument("--cli-checksums", type=Path)
    parser.add_argument("--verify-releases", action="store_true")
    arguments = parser.parse_args()
    declaration_path = arguments.declaration or (
        Path("release") / "compatibility" / f"{arguments.version}.json"
    )
    temporary_directory: tempfile.TemporaryDirectory[str] | None = None
    try:
        declaration = load_declaration(declaration_path, arguments.version)
        versions = component_versions(declaration)
        server_digest = arguments.server_image_digest or resolve_server_digest(arguments.version)
        checksums_path = arguments.cli_checksums
        if checksums_path is None:
            temporary_directory, checksums_path = download_cli_checksums(
                f"cli-v{versions['cli']}"
            )
        manifest = build_manifest(
            arguments.version,
            declaration_path,
            server_digest,
            checksums_path,
            arguments.verify_releases,
        )
        digest = write_manifest(arguments.output, manifest)
        print(f"manifest={arguments.output}")
        print(f"sha256={digest}")
        return 0
    except ManifestError as error:
        print(f"compatibility manifest failed: {error}", file=sys.stderr)
        return 1
    finally:
        if temporary_directory is not None:
            temporary_directory.cleanup()


if __name__ == "__main__":
    raise SystemExit(main())
