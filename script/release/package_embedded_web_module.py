# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Builds the self-contained github.com/superdurable/dex/web/embedded Go module from the web module.

The published module carries copies of the web packages it imports, rewritten under its internal/ directory,
plus the built SPA, and requires a released sdk-go version without replace directives.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
WEB_DIRECTORY = ROOT / "web"
WEB_MODULE = "github.com/superdurable/dex/web"
EMBEDDED_MODULE = WEB_MODULE + "/embedded"
SDK_GO_MODULE = "github.com/superdurable/dex/sdk-go"
WEB_IMPORT_PATTERN = re.compile(r'"github\.com/superdurable/dex/web((?:/[A-Za-z0-9._~-]+)*)"')
STABLE_SDK_GO_TAG_PATTERN = re.compile(r"^sdk-go/v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
NOTICE_FILES = ("LICENSE", "LEGACY_NOTICES.md", "LICENSES")


class PackagingError(Exception):
    pass


class UnreleasedSdkGoError(PackagingError):
    """The web code needs sdk-go changes that no sdk-go release contains yet."""


def build_module(output: Path, sdk_go_version: str) -> None:
    if output.exists() and any(output.iterdir()):
        raise PackagingError(f"Output directory is not empty: {output}")
    if not (WEB_DIRECTORY / "assets/dist/index.html").is_file():
        raise PackagingError("Build the SPA first: cd web && npm ci && npm run build")
    check_sdk_go_compatibility(sdk_go_version)
    output.mkdir(parents=True, exist_ok=True)
    for package in web_packages():
        copy_package(package, output)
    shutil.copytree(WEB_DIRECTORY / "assets/dist", output / "internal/web/assets/dist")
    for name in NOTICE_FILES:
        source = WEB_DIRECTORY / name
        if source.is_dir():
            shutil.copytree(source, output / name)
        else:
            shutil.copy2(source, output / name)
    write_go_mod(output, sdk_go_version)
    go_environment = {**os.environ, "GOWORK": "off", "GOFLAGS": "-mod=mod"}
    run(["go", "mod", "tidy"], output, go_environment)
    run(["go", "build", "./..."], output, go_environment)
    run(["go", "vet", "./..."], output, go_environment)


def newest_sdk_go_version() -> str:
    tags = run(["git", "tag", "--list", "sdk-go/v*"], ROOT).split()
    versions = [tuple(int(part) for part in match.groups()) for match in map(STABLE_SDK_GO_TAG_PATTERN.match, tags) if match]
    if not versions:
        raise PackagingError("No stable sdk-go/v* tag exists")
    return "v" + ".".join(str(part) for part in max(versions))


def check_sdk_go_compatibility(sdk_go_version: str) -> None:
    """Fails when the web code would compile against sdk-go packages that the released version lacks."""
    tag = "sdk-go/" + sdk_go_version
    run(["git", "rev-parse", "--verify", "--quiet", tag + "^{commit}"], ROOT)
    sdk_go_directories = sorted({
        "sdk-go/" + package.removeprefix(SDK_GO_MODULE + "/")
        for package in go_list_dependencies() if package.startswith(SDK_GO_MODULE + "/")
    })
    changed = run(["git", "diff", "--name-only", tag, "--", *sdk_go_directories], ROOT).strip()
    if changed:
        raise UnreleasedSdkGoError(f"Web uses sdk-go code that {tag} does not contain; release sdk-go first:\n{changed}")


def web_packages() -> list[dict]:
    packages = []
    for package in go_list_dependencies():
        if package == WEB_MODULE or package.startswith(WEB_MODULE + "/"):
            listed = json.loads(run(["go", "list", "-json", package], WEB_DIRECTORY, {**os.environ, "GOWORK": "off"}))
            packages.append(listed)
    return packages


def go_list_dependencies() -> list[str]:
    output = run(
        ["go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", "./embedded"],
        WEB_DIRECTORY,
        {**os.environ, "GOWORK": "off"},
    )
    return [line for line in output.splitlines() if line]


def published_directory(import_path: str) -> str:
    if import_path == EMBEDDED_MODULE:
        return "."
    return "internal/web" + import_path.removeprefix(WEB_MODULE)


def published_import_path(match: re.Match[str]) -> str:
    import_path = WEB_MODULE + match.group(1)
    if import_path == EMBEDDED_MODULE:
        return f'"{EMBEDDED_MODULE}"'
    return f'"{EMBEDDED_MODULE}/{published_directory(import_path)}"'


def copy_package(package: dict, output: Path) -> None:
    source = Path(package["Dir"])
    destination = output / published_directory(package["ImportPath"])
    destination.mkdir(parents=True, exist_ok=True)
    # Every non-test Go file keeps files for other operating systems and build tags.
    for file in sorted(source.glob("*.go")):
        if file.name.endswith("_test.go"):
            continue
        text = WEB_IMPORT_PATTERN.sub(published_import_path, file.read_text())
        (destination / file.name).write_text(text)


def write_go_mod(output: Path, sdk_go_version: str) -> None:
    web_go_mod = json.loads(run(["go", "mod", "edit", "-json"], WEB_DIRECTORY))
    lines = [f"module {EMBEDDED_MODULE}", "", f"go {web_go_mod['Go']}", "", "require ("]
    for requirement in web_go_mod.get("Require") or []:
        version = sdk_go_version if requirement["Path"] == SDK_GO_MODULE else requirement["Version"]
        lines.append(f"\t{requirement['Path']} {version}")
    lines.append(")")
    (output / "go.mod").write_text("\n".join(lines) + "\n")


def run(command: list[str], directory: Path, environment: dict[str, str] | None = None) -> str:
    result = subprocess.run(command, cwd=directory, env=environment, text=True, capture_output=True)
    if result.returncode != 0:
        raise PackagingError(f"{' '.join(command)} failed in {directory}:\n{result.stdout}{result.stderr}")
    return result.stdout


def main() -> int:
    parser = argparse.ArgumentParser(description="Build the github.com/superdurable/dex/web/embedded Go module")
    parser.add_argument("--output", type=Path, required=True, help="empty directory that receives the module")
    parser.add_argument("--sdk-go-version", default="", help="released sdk-go version, such as v1.43.0; default newest")
    arguments = parser.parse_args()
    try:
        sdk_go_version = arguments.sdk_go_version or newest_sdk_go_version()
        build_module(arguments.output.resolve(), sdk_go_version)
    except PackagingError as error:
        print(error, file=sys.stderr)
        return 1
    print(json.dumps({"module": EMBEDDED_MODULE, "sdkGoVersion": sdk_go_version, "directory": str(arguments.output)}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
