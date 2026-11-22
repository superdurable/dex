# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Checks that every agents Go module builds for a consumer from its release tag.

`go get` fetches github.com/superdurable/dex/agents/go/<module> from the tag agents/go/<module>/v<version>
and builds the go.mod of that commit without its replace and exclude directives. So each
agents/go/<module>/go.mod must:

- declare the module path github.com/superdurable/dex/agents/go/<module>, the only path its tag resolves;
- require every other github.com/superdurable module at a release: never at a pseudo-version, which
  names an unreleased commit such as an unreleased Dex SDK, and never through a replace;
- require each agents Go module at the agents release version, with the one replace
  `<module> => ../<module>` that builds the local module in this repository. The agents Go modules
  require each other at one version; --version names the one the release must carry;
- have no other replace or exclude directive.

A required release that does not exist fails `go mod verify` in the module's ci.sh, which downloads it,
so this check reads only the go.mod files. With --version it prints the Go tag of each module, one per
line. The go.mod parsing matches agents/script/stamp_agents_release_version.py --go, which writes the
release version.
"""

from __future__ import annotations

import argparse
import re
import sys
from collections.abc import Iterator
from dataclasses import dataclass
from pathlib import Path

REPOSITORY = Path(__file__).resolve().parents[2]
AGENTS_GO_DIRECTORY = "agents/go"
AGENTS_MODULE_PREFIX = "github.com/superdurable/dex/agents/go/"
SUPERDURABLE_MODULE_PREFIX = "github.com/superdurable/"
# A version a go.mod may require: semantic version 2.0.0 with a "v" prefix and no build metadata.
GO_IDENTIFIER = r"(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
GO_VERSION_PATTERN = re.compile(
    rf"^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-{GO_IDENTIFIER}(?:\.{GO_IDENTIFIER})*)?$"
)
# The pseudo-version forms of golang.org/x/mod/module: a commit, not a release.
GO_PSEUDO_VERSION_PATTERN = re.compile(r"^v[0-9]+\.(0\.0-|[0-9]+\.[0-9]+-([^+]*\.)?0\.)[0-9]{14}-[A-Za-z0-9]+$")
GO_MOD_TOKEN_PATTERN = re.compile(r'"(?:[^"\\]|\\.)*"|`[^`]*`|[()]|[^\s()"`]+')


@dataclass(frozen=True)
class GoMod:
    """The directives of one go.mod this check reads, each as its unquoted fields."""

    path: Path
    module: str | None
    requires: tuple[tuple[str, ...], ...]
    replaces: tuple[tuple[str, ...], ...]
    excludes: tuple[tuple[str, ...], ...]


def go_mod_directives(text: str) -> Iterator[tuple[str, list[str]]]:
    """Yields the verb and unquoted fields of each go.mod directive, one per block entry."""
    block_verb: str | None = None
    for line in text.split("\n"):
        fields = [
            token[1:-1] if token[:1] in ('"', "`") else token
            for token in GO_MOD_TOKEN_PATTERN.findall(line.split("//", 1)[0])
        ]
        if not fields:
            continue
        if block_verb is not None:
            if fields == [")"]:
                block_verb = None
            else:
                yield block_verb, fields
        elif fields[1:] == ["("]:
            block_verb = fields[0]
        elif fields[1:] != ["(", ")"]:
            yield fields[0], fields[1:]


def read_go_mod(path: Path) -> GoMod:
    directives = list(go_mod_directives(path.read_text(encoding="utf-8")))
    return GoMod(
        path=path,
        module=next((fields[0] for verb, fields in directives if verb == "module" and fields), None),
        requires=tuple(tuple(fields) for verb, fields in directives if verb == "require" and fields),
        replaces=tuple(tuple(fields) for verb, fields in directives if verb == "replace" and fields),
        excludes=tuple(tuple(fields) for verb, fields in directives if verb == "exclude" and fields),
    )


def agents_go_mods(repository: Path) -> list[GoMod]:
    return [read_go_mod(path) for path in sorted((repository / AGENTS_GO_DIRECTORY).glob("*/go.mod"))]


def go_module_tags(repository: Path, version: str) -> list[str]:
    """Returns the release tag of each agents Go module, such as agents/go/core/v0.6.16."""
    return [f"{AGENTS_GO_DIRECTORY}/{go_mod.path.parent.name}/v{version}" for go_mod in agents_go_mods(repository)]


def is_release_version(version: str) -> bool:
    return bool(GO_VERSION_PATTERN.fullmatch(version)) and not GO_PSEUDO_VERSION_PATTERN.fullmatch(version)


def release_version_errors(go_version: str) -> list[str]:
    if not is_release_version(go_version):
        return [f"{go_version} is not a Go module release version"]
    major = int(GO_VERSION_PATTERN.fullmatch(go_version).group(1))
    if major >= 2:
        return [
            f"{go_version}: the agents Go module paths have no /v{major} suffix, "
            "so a release from 2.0.0 on needs new module paths first"
        ]
    return []


def go_module_errors(repository: Path, version: str | None = None) -> list[str]:
    """Returns why an agents Go module would not build for a consumer from its tag, or no errors."""
    go_mods = agents_go_mods(repository)
    known_modules = {AGENTS_MODULE_PREFIX + go_mod.path.parent.name for go_mod in go_mods}
    release_version = None if version is None else f"v{version}"
    errors = [] if release_version is None else release_version_errors(release_version)
    agents_requires: dict[str, list[str]] = {}
    for go_mod in go_mods:
        name = go_mod.path.relative_to(repository).as_posix()
        expected_module = AGENTS_MODULE_PREFIX + go_mod.path.parent.name
        if go_mod.module != expected_module:
            errors.append(
                f"{name}: declares module {go_mod.module}, but its tag "
                f"{AGENTS_GO_DIRECTORY}/{go_mod.path.parent.name}/v<version> resolves only {expected_module}"
            )
        local_replaces: list[tuple[str, ...]] = []
        for fields in go_mod.requires:
            module, required = fields[0], fields[1] if len(fields) > 1 else ""
            if module.startswith(AGENTS_MODULE_PREFIX):
                local_replace = (module, "=>", f"../{module.removeprefix(AGENTS_MODULE_PREFIX)}")
                local_replaces.append(local_replace)
                agents_requires.setdefault(required, []).append(f"{name} requires {module} {required}")
                if module not in known_modules:
                    errors.append(f"{name}: requires {module}, which is not a module under {AGENTS_GO_DIRECTORY}")
                if local_replace not in go_mod.replaces:
                    errors.append(
                        f"{name}: requires {module} without replace {' '.join(local_replace)}, "
                        "so this repository would not build the local module"
                    )
                if not is_release_version(required):
                    errors.append(f"{name}: requires {module} {required}, which is not a release version")
                elif release_version is not None and required != release_version:
                    errors.append(
                        f"{name}: requires {module} {required}, not the release version {release_version}; "
                        f"run python3 agents/script/stamp_agents_release_version.py {version} --go"
                    )
            elif module.startswith(SUPERDURABLE_MODULE_PREFIX):
                if GO_PSEUDO_VERSION_PATTERN.fullmatch(required):
                    errors.append(
                        f"{name}: requires {module} at the pseudo-version {required}, an unreleased commit; "
                        "require a released version"
                    )
                elif not is_release_version(required):
                    errors.append(f"{name}: requires {module} {required}, which is not a release version")
        for fields in go_mod.replaces:
            if fields not in local_replaces:
                errors.append(
                    f"{name}: replace {' '.join(fields)}: a consumer builds without replace directives; only "
                    "a required agents Go module may be replaced, by its local directory"
                )
        for fields in go_mod.excludes:
            errors.append(f"{name}: exclude {' '.join(fields)}: a consumer builds without exclude directives")
    if release_version is None and len(agents_requires) > 1:
        requires = sorted(require for group in agents_requires.values() for require in group)
        errors.append("the agents Go modules require each other at different versions: " + "; ".join(requires))
    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument(
        "--version", help="agents release version, such as 0.6.16; every agents Go module must require the others at it"
    )
    parser.add_argument("--repository", type=Path, default=REPOSITORY, help="repository root; default this checkout")
    arguments = parser.parse_args()
    repository = arguments.repository.resolve()
    errors = go_module_errors(repository, arguments.version)
    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1
    if arguments.version is None:
        count = len(agents_go_mods(repository))
        print(f"checked {count} agents Go module{'' if count == 1 else 's'}")
    else:
        print("\n".join(go_module_tags(repository, arguments.version)))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
