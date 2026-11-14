# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Writes and checks the server-zd compatibility file that the docs site publishes."""

from __future__ import annotations

import argparse
import datetime
import json
import os
import re
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Callable, Mapping, Sequence

from component_changelogs import TARGETS as COMPONENT_CHANGELOGS

COMPATIBILITY_FILE = "docs/static/compatibility/server-zd.json"
SCHEMA_URL = "https://docs.superdurable.io/compatibility/server-zd.schema.json"
RULES_FILE = "script/release/server_zd_compatibility_rules.json"
SCHEMA_VERSION = 1
SERVER_CHANGELOG = "server"
SERVER_TAG_PREFIX = "server-zd/v"
SERVER_PROTOCOL_SOURCE = "server-zd/crates/dexzd-api/src/flow_service.rs"
SERVER_MINIMUM_PROTOCOL = re.compile(r"pub const MINIMUM_SUPPORTED_PROTOCOL_VERSION: u32 = (\d+);")
SERVER_MAXIMUM_PROTOCOL = re.compile(r"pub const CURRENT_PROTOCOL_VERSION: u32 = (\d+);")
CLIENT_MINIMUM_PROTOCOL = re.compile(r"minimum_?supported_?server_?protocol_?version\b[^=\n]*=\s*(\d+)", re.IGNORECASE)
CLIENT_MAXIMUM_PROTOCOL = re.compile(r"maximum_?supported_?server_?protocol_?version\b[^=\n]*=\s*(\d+)", re.IGNORECASE)
RELEASED_LINE_MENTION = re.compile(r"[Rr]eleased (?P<date>\d{4}-\d{2}-\d{2}) as \[(?P<tag>[^\]]+)\]")
SEMVER = re.compile(r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$")
PYTHON_SDK_PIN = re.compile(r'"dex-python-sdk==(?P<version>[^"]+)"')
NPM_SDK_PACKAGE = "@superdurable/dex"
AGENTS_REGISTRIES = ("npm", "pypi")
# The registry SDK that an agents package on each registry pins.
AGENTS_PINNED_SDK = {"npm": "typescript", "pypi": "python"}
HTTP_USER_AGENT = "dex-release-tooling (server-zd compatibility check)"
HELP_EPILOG = """\
For each server-zd release from the rules' oldestSupportedServerZd on, the file lists the oldest and the
newest release of each client that works with it: the five SDKs, dexcli, the Dex Agents packages, and the
Dex Agents CLI. The inputs are the component change logs, the release tags, and the hand-maintained rules
in script/release/server_zd_compatibility_rules.json.

A client release works with a server-zd release when both are true:
- their protocol ranges overlap: client minimum <= server maximum and server minimum <= client maximum;
- the server is at least the client line's minimumServerZd, when the line names one.
The Dex Agents packages and the Agents CLI take the protocol range of the SDK they pin on each registry.
"""


@dataclass(frozen=True)
class SdkClient:
    """One client whose protocol range lives in a source file at its release tags."""

    name: str
    changelog: str
    protocol_source: str
    registry: str


SDK_CLIENTS = (
    SdkClient("go", "go", "sdk-go/dex/server_protocol.go", "goproxy"),
    SdkClient("python", "python", "sdk-python/dex/_server_protocol.py", "pypi"),
    SdkClient("typescript", "typescript", "sdk-typescript/src/server-protocol.ts", "npm"),
    SdkClient("java", "java", "sdk-java/src/main/java/io/superdurable/dex/ServerProtocolCompatibility.java", "maven"),
    SdkClient("rust", "rust", "sdk-rust/crates/dex-sdk/src/server_protocol.rs", "crates"),
)
# dexcli builds against the Go SDK of its own commit.
DEXCLI = SdkClient("dexcli", "cli", "sdk-go/dex/server_protocol.go", "github")
PROTOCOL_CLIENTS = (*SDK_CLIENTS, DEXCLI)
AGENTS_CHANGELOG = "agents"
# Where each agents release pins its SDK: the library core and the CLI core.
AGENTS_PIN_SOURCES = {
    "agents": {"npm": "agents/typescript/core/package.json", "pypi": "agents/python/core/pyproject.toml"},
    "agentsCli": {"npm": "agents/cli/typescript/core/package.json", "pypi": "agents/cli/python/core/pyproject.toml"},
}
# The package each registry check reads for a component.
REGISTRY_PACKAGES = {
    ("go", "goproxy"): "github.com/superdurable/dex/sdk-go",
    ("python", "pypi"): "dex-python-sdk",
    ("typescript", "npm"): NPM_SDK_PACKAGE,
    ("java", "maven"): "io.superdurable:dex-sdk",
    ("rust", "crates"): "dex-sdk",
    ("dexcli", "github"): "cli-v",
    ("agents", "npm"): "@superdurable/dex-agents",
    ("agents", "pypi"): "dex-agents",
    ("agentsCli", "npm"): "@superdurable/dex-agents-cli-core",
    ("agentsCli", "pypi"): "dex-agents-cli-core",
}


class CompatibilityError(Exception):
    """The rules, the change logs, the tags, or the compatibility file are inconsistent."""


@dataclass(frozen=True)
class ProtocolRange:
    minimum: int
    maximum: int

    def overlaps(self, other: ProtocolRange) -> bool:
        return self.minimum <= other.maximum and other.minimum <= self.maximum


@dataclass(frozen=True)
class RuleLine:
    """One release line: it applies from since up to the next line of the same component."""

    since: str
    protocol: ProtocolRange | None
    minimum_server: str | None


@dataclass(frozen=True)
class Rules:
    oldest_supported_server: str
    server_lines: tuple[RuleLine, ...]
    client_lines: Mapping[str, tuple[RuleLine, ...]]
    unpublished: frozenset[tuple[str, str, str]]


class RepositoryReader:
    """Reads files of the working tree or of a release tag."""

    def __init__(self, repository: Path) -> None:
        self.repository = repository
        self._tags: frozenset[str] | None = None

    def working_file(self, path: str) -> str | None:
        file_path = self.repository / path
        return file_path.read_text(encoding="utf-8") if file_path.is_file() else None

    def tags(self) -> frozenset[str]:
        if self._tags is None:
            result = run_git(self.repository, "tag", "--list")
            self._tags = frozenset(result.stdout.split())
        return self._tags

    def tag_file(self, tag: str, path: str) -> str | None:
        """Returns the file at tag, or None when the tag has no such file."""
        result = run_git(self.repository, "show", f"{tag}:{path}", check=False)
        return result.stdout if result.returncode == 0 else None


def main(arguments: Sequence[str] | None = None) -> int:
    parser = build_parser()
    options = parser.parse_args(arguments)
    try:
        return options.handler(options, repository_root())
    except CompatibilityError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=__doc__, epilog=HELP_EPILOG, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    commands = parser.add_subparsers(required=True)
    update = commands.add_parser("update", help="rewrite the compatibility file from the change logs, tags, and rules")
    update.add_argument("--date", help="generatedAt when the content changes, YYYY-MM-DD; defaults to today in UTC")
    update.set_defaults(handler=run_update)
    check = commands.add_parser("check", help="fail unless the file, rules, tags, and working tree agree; offline")
    check.set_defaults(handler=run_check)
    registries = commands.add_parser("verify-registries", help="fail unless every listed version is published")
    registries.add_argument("--attempts", type=int, default=3, help="tries per missing version, for publication delays")
    registries.add_argument("--delay", type=float, default=20.0, help="seconds between tries")
    registries.set_defaults(handler=run_verify_registries)
    return parser


def run_update(options: argparse.Namespace, repository: Path) -> int:
    changed = update_compatibility_file(repository, options.date)
    print(f"{COMPATIBILITY_FILE} {'updated' if changed else 'unchanged'}")
    return 0


def update_compatibility_file(repository: Path, release_date: str | None) -> bool:
    """Rewrites the compatibility file and returns whether its content changed.

    generatedAt keeps its old value unless the servers change; then it becomes release_date, or today.
    """
    reader = RepositoryReader(repository)
    servers = compute_servers(reader, load_rules(reader))
    path = repository / COMPATIBILITY_FILE
    existing = json.loads(path.read_text(encoding="utf-8")) if path.is_file() else None
    if existing is not None and existing.get("servers") == servers and existing.get("schemaVersion") == SCHEMA_VERSION:
        generated_at = existing["generatedAt"]
    else:
        generated_at = release_date or datetime.datetime.now(datetime.timezone.utc).date().isoformat()
    validate_date(generated_at)
    text = render_document(generated_at, servers)
    if path.is_file() and path.read_text(encoding="utf-8") == text:
        return False
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")
    return True


def run_check(_options: argparse.Namespace, repository: Path) -> int:
    reader = RepositoryReader(repository)
    errors = check_errors(reader)
    if errors:
        for error in errors:
            print(f"error: {error}", file=sys.stderr)
        return 1
    print(f"{COMPATIBILITY_FILE} matches the change logs, the tags, and {RULES_FILE}")
    return 0


def check_errors(reader: RepositoryReader) -> list[str]:
    """Returns every reason the compatibility file or its rules are out of date, or no reasons."""
    rules = load_rules(reader)
    errors = rule_tag_errors(reader, rules) + rule_working_tree_errors(reader, rules)
    errors.extend(server_tag_coverage_errors(reader, rules))
    text = reader.working_file(COMPATIBILITY_FILE)
    if text is None:
        return [*errors, f"{COMPATIBILITY_FILE} is missing. Run: python3 script/release/server_zd_compatibility.py update"]
    try:
        document = json.loads(text)
    except json.JSONDecodeError as error:
        return [*errors, f"{COMPATIBILITY_FILE} is not JSON: {error}"]
    generated_at = document.get("generatedAt")
    if not isinstance(generated_at, str) or not is_date(generated_at):
        errors.append(f"{COMPATIBILITY_FILE}: generatedAt must be a YYYY-MM-DD date")
        generated_at = "1970-01-01"
    servers = compute_servers(reader, rules)
    if text != render_document(generated_at, servers):
        errors.append(
            f"{COMPATIBILITY_FILE} differs from the generator output. "
            "Run: python3 script/release/server_zd_compatibility.py update"
        )
    return errors


def load_rules(reader: RepositoryReader) -> Rules:
    text = reader.working_file(RULES_FILE)
    if text is None:
        raise CompatibilityError(f"{RULES_FILE} is missing")
    data = json.loads(text)
    oldest_supported_server = require_version(data.get("oldestSupportedServerZd"), "oldestSupportedServerZd")
    server_lines = parse_lines(data.get("serverZd"), "serverZd", is_protocol_required=True)
    client_lines: dict[str, tuple[RuleLine, ...]] = {}
    clients = data.get("clients") or {}
    for client in PROTOCOL_CLIENTS:
        client_lines[client.name] = parse_lines(clients.get(client.name), f"clients.{client.name}", is_protocol_required=True)
    for name in AGENTS_PIN_SOURCES:
        lines = parse_lines(clients.get(name), f"clients.{name}", is_protocol_required=False)
        if any(line.protocol is not None for line in lines):
            raise CompatibilityError(f"{RULES_FILE}: clients.{name} takes its protocol from the SDK it pins; remove protocol")
        client_lines[name] = lines
    unknown = sorted(set(clients) - set(client_lines))
    if unknown:
        raise CompatibilityError(f"{RULES_FILE}: unknown clients {unknown}")
    unpublished = set()
    for index, entry in enumerate(data.get("unpublished") or []):
        component, registry = entry.get("component"), entry.get("registry")
        version = require_version(entry.get("version"), f"unpublished[{index}].version")
        if (component, registry) not in REGISTRY_PACKAGES:
            raise CompatibilityError(f"{RULES_FILE}: unpublished[{index}] names no known component and registry")
        unpublished.add((component, registry, version))
    return Rules(oldest_supported_server, server_lines, client_lines, frozenset(unpublished))


def parse_lines(entries: object, label: str, is_protocol_required: bool) -> tuple[RuleLine, ...]:
    if not isinstance(entries, list) or not entries:
        raise CompatibilityError(f"{RULES_FILE}: {label} must be a non-empty list of lines")
    lines = []
    for index, entry in enumerate(entries):
        since = require_version(entry.get("since"), f"{label}[{index}].since")
        protocol = entry.get("protocol")
        if protocol is None and is_protocol_required:
            raise CompatibilityError(f"{RULES_FILE}: {label}[{index}] needs a protocol range")
        protocol_range = None
        if protocol is not None:
            minimum, maximum = protocol.get("min"), protocol.get("max")
            if not isinstance(minimum, int) or not isinstance(maximum, int) or not 0 < minimum <= maximum:
                raise CompatibilityError(f"{RULES_FILE}: {label}[{index}].protocol needs 0 < min <= max")
            protocol_range = ProtocolRange(minimum, maximum)
        minimum_server = entry.get("minimumServerZd")
        if minimum_server is not None:
            require_version(minimum_server, f"{label}[{index}].minimumServerZd")
        lines.append(RuleLine(since, protocol_range, minimum_server))
    for earlier, later in zip(lines, lines[1:]):
        if version_key(earlier.since) >= version_key(later.since):
            raise CompatibilityError(f"{RULES_FILE}: {label} lines must be in increasing since order")
    return tuple(lines)


def compute_servers(reader: RepositoryReader, rules: Rules) -> list[dict]:
    """Returns one entry per server-zd release from oldestSupportedServerZd on, newest first."""
    servers = [
        version
        for version in released_versions(reader, SERVER_CHANGELOG)
        if version_key(version) >= version_key(rules.oldest_supported_server)
    ]
    if not servers:
        raise CompatibilityError(f"the server change log records no release from {rules.oldest_supported_server} on")
    releases = {client.name: released_versions(reader, client.changelog) for client in PROTOCOL_CLIENTS}
    agents_releases = released_versions(reader, AGENTS_CHANGELOG)
    pins = AgentsPins(reader, agents_releases)
    entries = []
    for server in sorted(servers, key=version_key, reverse=True):
        server_line = line_for(rules.server_lines, server)
        if server_line is None or server_line.protocol is None:
            raise CompatibilityError(f"{RULES_FILE}: no serverZd line covers server-zd {server}")
        context = ServerContext(server, server_line.protocol)
        entries.append(
            {
                "serverZd": server,
                "protocolVersion": {"min": server_line.protocol.minimum, "max": server_line.protocol.maximum},
                "sdks": {
                    client.name: client_range(rules, client, releases[client.name], context) for client in SDK_CLIENTS
                },
                "dexcli": client_range(rules, DEXCLI, releases[DEXCLI.name], context),
                "agents": {
                    registry: agents_range(rules, "agents", registry, agents_releases, pins, context, is_pin_listed=True)
                    for registry in AGENTS_REGISTRIES
                },
                "agentsCli": {
                    registry: agents_range(rules, "agentsCli", registry, agents_releases, pins, context, is_pin_listed=False)
                    for registry in AGENTS_REGISTRIES
                },
            }
        )
    return entries


@dataclass(frozen=True)
class ServerContext:
    version: str
    protocol: ProtocolRange


def client_range(rules: Rules, client: SdkClient, releases: Sequence[str], context: ServerContext) -> dict | None:
    """Returns the oldest and newest release of client that works with the server, or None when none does.

    The oldest is the first version of the earliest compatible rule line, since change logs miss early releases.
    The newest is the newest compatible release that the change log records.
    """
    lines = rules.client_lines[client.name]
    compatible_lines = [line for line in lines if is_line_compatible(line, line.protocol, context)]
    if not compatible_lines:
        return None
    oldest = compatible_lines[0].since
    if (client.name, client.registry, oldest) in rules.unpublished:
        raise CompatibilityError(f"{RULES_FILE}: line {client.name} {oldest} starts at an unpublished version")
    newest = None
    for version in sorted(releases, key=version_key, reverse=True):
        if (client.name, client.registry, version) in rules.unpublished:
            continue
        line = line_for(lines, version)
        if line is not None and is_line_compatible(line, line.protocol, context):
            newest = version
            break
    if newest is None or version_key(newest) < version_key(oldest):
        raise CompatibilityError(
            f"the {client.changelog} change log records no {client.name} release that works with server-zd {context.version}"
        )
    return {"oldest": oldest, "newest": newest}


def agents_range(
    rules: Rules,
    component: str,
    registry: str,
    releases: Sequence[str],
    pins: AgentsPins,
    context: ServerContext,
    is_pin_listed: bool,
) -> dict | None:
    """Returns the oldest and newest agents release on registry that works with the server, or None."""
    lines = rules.client_lines[component]
    sdk_lines = rules.client_lines[AGENTS_PINNED_SDK[registry]]
    compatible = []
    for version in sorted(releases, key=version_key):
        line = line_for(lines, version)
        if line is None or (component, registry, version) in rules.unpublished:
            continue
        pinned_sdk = pins.pinned_sdk(component, registry, version)
        sdk_line = line_for(sdk_lines, pinned_sdk)
        if sdk_line is not None and is_line_compatible(line, sdk_line.protocol, context):
            compatible.append((version, pinned_sdk))
    if not compatible:
        return None
    if not is_pin_listed:
        return {"oldest": compatible[0][0], "newest": compatible[-1][0]}
    return {
        "oldest": {"version": compatible[0][0], "pinnedSdk": compatible[0][1]},
        "newest": {"version": compatible[-1][0], "pinnedSdk": compatible[-1][1]},
    }


def is_line_compatible(line: RuleLine, protocol: ProtocolRange | None, context: ServerContext) -> bool:
    if protocol is None or not protocol.overlaps(context.protocol):
        return False
    return line.minimum_server is None or version_key(context.version) >= version_key(line.minimum_server)


class AgentsPins:
    """Reads the SDK version each agents release pins on each registry."""

    def __init__(self, reader: RepositoryReader, releases: Sequence[str]) -> None:
        self.reader = reader
        self.newest = max(releases, key=version_key) if releases else None
        self._cache: dict[tuple[str, str, str], str] = {}

    def pinned_sdk(self, component: str, registry: str, version: str) -> str:
        key = (component, registry, version)
        if key not in self._cache:
            self._cache[key] = self._read_pin(component, registry, version)
        return self._cache[key]

    def _read_pin(self, component: str, registry: str, version: str) -> str:
        path = AGENTS_PIN_SOURCES[component][registry]
        tag = COMPONENT_CHANGELOGS[AGENTS_CHANGELOG].tag(version)
        if tag in self.reader.tags():
            text = self.reader.tag_file(tag, path)
            source = f"{tag}:{path}"
        elif version == self.newest:
            # A release-prep pull request records the newest release before its tag exists.
            text = self.reader.working_file(path)
            source = path
        else:
            raise CompatibilityError(f"tag {tag} is missing. Fetch the release tags: git fetch --tags origin")
        if text is None:
            raise CompatibilityError(f"{source} is missing, so the SDK that agents {version} pins is unknown")
        return parse_pinned_sdk(registry, text, source)


def parse_pinned_sdk(registry: str, text: str, source: str) -> str:
    if registry == "npm":
        manifest = json.loads(text)
        pins = {
            manifest.get(section, {}).get(NPM_SDK_PACKAGE)
            for section in ("dependencies", "peerDependencies")
            if manifest.get(section, {}).get(NPM_SDK_PACKAGE)
        }
    else:
        pins = {match.group("version") for match in PYTHON_SDK_PIN.finditer(text)}
    if len(pins) != 1 or not SEMVER.match(next(iter(pins))):
        raise CompatibilityError(f"{source} must pin exactly one SDK version, found {sorted(pins)}")
    return next(iter(pins))


def rule_tag_errors(reader: RepositoryReader, rules: Rules) -> list[str]:
    """Returns where a release tag's protocol constants disagree with the rule line of its version."""
    errors = []
    checks = [(client.name, COMPONENT_CHANGELOGS[client.changelog].tag_prefix, client.protocol_source) for client in PROTOCOL_CLIENTS]
    checks.append(("serverZd", SERVER_TAG_PREFIX, SERVER_PROTOCOL_SOURCE))
    for name, prefix, source in checks:
        lines = rules.server_lines if name == "serverZd" else rules.client_lines[name]
        for version in tag_versions(reader, prefix):
            line = line_for(lines, version)
            expected = line.protocol if line is not None else None
            text = reader.tag_file(f"{prefix}{version}", source)
            actual = read_protocol(name, text) if text is not None else None
            if actual != expected:
                errors.append(
                    f"{RULES_FILE}: {name} {version} has protocol {describe(actual)} at tag {prefix}{version}, "
                    f"but its rule line says {describe(expected)}"
                )
    return errors


def rule_working_tree_errors(reader: RepositoryReader, rules: Rules) -> list[str]:
    """Returns where the newest rule line of a component disagrees with the working tree."""
    errors = []
    checks = [(client.name, client.protocol_source) for client in PROTOCOL_CLIENTS] + [("serverZd", SERVER_PROTOCOL_SOURCE)]
    for name, source in checks:
        lines = rules.server_lines if name == "serverZd" else rules.client_lines[name]
        text = reader.working_file(source)
        actual = read_protocol(name, text) if text is not None else None
        if actual != lines[-1].protocol:
            errors.append(
                f"{source} states protocol {describe(actual)}, but the newest {name} line in {RULES_FILE} says "
                f"{describe(lines[-1].protocol)}. Add a line whose since is the next {name} release"
            )
    return errors


def server_tag_coverage_errors(reader: RepositoryReader, rules: Rules) -> list[str]:
    recorded = set(released_versions(reader, SERVER_CHANGELOG))
    return [
        f"tag {SERVER_TAG_PREFIX}{version} exists, but the server change log does not record it"
        for version in tag_versions(reader, SERVER_TAG_PREFIX)
        if version_key(version) >= version_key(rules.oldest_supported_server) and version not in recorded
    ]


def read_protocol(name: str, text: str) -> ProtocolRange | None:
    minimum_pattern, maximum_pattern = (
        (SERVER_MINIMUM_PROTOCOL, SERVER_MAXIMUM_PROTOCOL)
        if name == "serverZd"
        else (CLIENT_MINIMUM_PROTOCOL, CLIENT_MAXIMUM_PROTOCOL)
    )
    minimum, maximum = minimum_pattern.search(text), maximum_pattern.search(text)
    if minimum is None or maximum is None:
        return None
    return ProtocolRange(int(minimum.group(1)), int(maximum.group(1)))


def describe(protocol: ProtocolRange | None) -> str:
    return "none" if protocol is None else f"{protocol.minimum}..{protocol.maximum}"


def released_versions(reader: RepositoryReader, changelog_name: str) -> list[str]:
    """Returns every version whose Released line the component change log records."""
    target = COMPONENT_CHANGELOGS[changelog_name]
    text = reader.working_file(target.output)
    if text is None:
        raise CompatibilityError(f"{target.output} is missing")
    versions = set()
    for line in text.splitlines():
        if not line.startswith("Released "):
            continue
        for mention in RELEASED_LINE_MENTION.finditer(line):
            tag = mention.group("tag")
            if tag.startswith(target.tag_prefix) and SEMVER.match(tag[len(target.tag_prefix) :]):
                versions.add(tag[len(target.tag_prefix) :])
    return sorted(versions, key=version_key)


def tag_versions(reader: RepositoryReader, prefix: str) -> list[str]:
    return sorted(
        (tag[len(prefix) :] for tag in reader.tags() if tag.startswith(prefix) and SEMVER.match(tag[len(prefix) :])),
        key=version_key,
    )


def line_for(lines: Sequence[RuleLine], version: str) -> RuleLine | None:
    """Returns the rule line that covers version, or None before the first line."""
    covering = None
    for line in lines:
        if version_key(line.since) <= version_key(version):
            covering = line
    return covering


def render_document(generated_at: str, servers: list[dict]) -> str:
    document = {"$schema": SCHEMA_URL, "schemaVersion": SCHEMA_VERSION, "generatedAt": generated_at, "servers": servers}
    return json.dumps(document, indent=2) + "\n"


def run_verify_registries(options: argparse.Namespace, repository: Path) -> int:
    reader = RepositoryReader(repository)
    text = reader.working_file(COMPATIBILITY_FILE)
    if text is None:
        raise CompatibilityError(f"{COMPATIBILITY_FILE} is missing")
    expected = published_versions(json.loads(text))
    rules = load_rules(reader)
    errors = registry_errors(expected, rules.unpublished, RegistryClient(options.attempts, options.delay))
    if errors:
        for error in errors:
            print(f"error: {error}", file=sys.stderr)
        return 1
    print(f"all {len(expected)} versions in {COMPATIBILITY_FILE} are published")
    return 0


def published_versions(document: Mapping) -> list[tuple[str, str, str]]:
    """Returns each (component, registry, version) that the document says one can install."""
    found = set()
    # Server releases are left out: a release-prep entry precedes its tag, and check-ready guards the tag.
    for server in document["servers"]:
        for client in SDK_CLIENTS:
            add_range(found, client.name, client.registry, server["sdks"][client.name])
        add_range(found, DEXCLI.name, DEXCLI.registry, server["dexcli"])
        for registry in AGENTS_REGISTRIES:
            agents = server["agents"][registry]
            if agents is not None:
                for end in ("oldest", "newest"):
                    found.add(("agents", registry, agents[end]["version"]))
                    found.add((AGENTS_PINNED_SDK[registry], registry, agents[end]["pinnedSdk"]))
            add_range(found, "agentsCli", registry, server["agentsCli"][registry])
    return sorted(found)


def add_range(found: set[tuple[str, str, str]], component: str, registry: str, version_range: Mapping | None) -> None:
    if version_range is not None:
        found.add((component, registry, version_range["oldest"]))
        found.add((component, registry, version_range["newest"]))


def registry_errors(
    expected: Sequence[tuple[str, str, str]], unpublished: frozenset[tuple[str, str, str]], client: RegistryClient
) -> list[str]:
    errors = []
    for component, registry, version in expected:
        if not client.exists(component, registry, version, should_wait=True):
            errors.append(f"{component} {version} is not published on {registry}")
    for component, registry, version in sorted(unpublished):
        if client.exists(component, registry, version, should_wait=False):
            errors.append(f"{component} {version} is published on {registry}; remove its unpublished entry from {RULES_FILE}")
    return errors


class RegistryClient:
    """Asks a package registry whether one version is published, retrying for publication delays."""

    def __init__(self, attempts: int, delay: float, fetch: Callable[[str, Mapping[str, str]], int] | None = None) -> None:
        self.attempts = max(1, attempts)
        self.delay = delay
        self.fetch = fetch or http_status

    def exists(self, component: str, registry: str, version: str, should_wait: bool) -> bool:
        """Returns whether the version is published.

        Every try retries a network failure or a server error. With should_wait, a 404 is retried too,
        for a release the registry has not shown yet; without it, a 404 means not published.
        Raises CompatibilityError when every try fails without an answer.
        """
        url, headers = registry_request(component, registry, version)
        status = 0
        for attempt in range(self.attempts):
            status = self.fetch(url, headers)
            if status == 200:
                return True
            if status == 404 and not should_wait:
                return False
            if attempt + 1 < self.attempts:
                time.sleep(self.delay)
        if status == 404:
            return False
        raise CompatibilityError(f"{url} answered {status}; cannot tell whether {component} {version} is published")


def registry_request(component: str, registry: str, version: str) -> tuple[str, dict[str, str]]:
    package = REGISTRY_PACKAGES[(component, registry)]
    headers = {"User-Agent": HTTP_USER_AGENT}
    if registry == "goproxy":
        return f"https://proxy.golang.org/{package}/@v/v{version}.info", headers
    if registry == "pypi":
        return f"https://pypi.org/pypi/{package}/{version}/json", headers
    if registry == "npm":
        return f"https://registry.npmjs.org/{urllib.parse.quote(package, safe='@')}/{version}", headers
    if registry == "maven":
        group, artifact = package.split(":")
        return f"https://repo1.maven.org/maven2/{group.replace('.', '/')}/{artifact}/{version}/{artifact}-{version}.pom", headers
    if registry == "crates":
        return f"https://crates.io/api/v1/crates/{package}/{version}", headers
    tag = urllib.parse.quote(f"{package}{version}", safe="")
    token = github_token()
    if token:
        headers["Authorization"] = f"Bearer {token}"
    return f"https://api.github.com/repos/superdurable/dex/releases/tags/{tag}", headers


def github_token() -> str | None:
    return os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN")


def http_status(url: str, headers: Mapping[str, str]) -> int:
    """Returns the HTTP status of a GET; 0 for a network failure, which callers retry."""
    request = urllib.request.Request(url, headers=dict(headers))
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return response.status
    except urllib.error.HTTPError as error:
        return error.code
    except (urllib.error.URLError, TimeoutError) as error:
        print(f"warning: {url}: {error}", file=sys.stderr)
        return 0


def require_version(value: object, label: str) -> str:
    if not isinstance(value, str) or not SEMVER.match(value):
        raise CompatibilityError(f"{RULES_FILE}: {label} must be a X.Y.Z version")
    return value


def validate_date(text: str) -> None:
    if not is_date(text):
        raise CompatibilityError(f"generatedAt must be a YYYY-MM-DD date: {text!r}")


def is_date(text: str) -> bool:
    if not re.fullmatch(r"\d{4}-\d{2}-\d{2}", text):
        return False
    try:
        datetime.date.fromisoformat(text)
    except ValueError:
        return False
    return True


def version_key(version: str) -> tuple[int, int, int]:
    match = SEMVER.match(version)
    if match is None:
        raise CompatibilityError(f"not a X.Y.Z version: {version!r}")
    return int(match.group(1)), int(match.group(2)), int(match.group(3))


def repository_root() -> Path:
    return Path(run_git(Path.cwd(), "rev-parse", "--show-toplevel").stdout.strip())


def run_git(repository: Path, *arguments: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(["git", "-C", str(repository), *arguments], capture_output=True, text=True, check=False)
    if check and result.returncode != 0:
        raise CompatibilityError(f"git {' '.join(arguments)} failed: {result.stderr.strip()}")
    return result


if __name__ == "__main__":
    sys.exit(main())
