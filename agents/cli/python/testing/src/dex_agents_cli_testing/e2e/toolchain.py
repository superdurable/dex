# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Build the commands from a checkout, as each ``ci.sh`` does, or install a release."""

from __future__ import annotations

import enum
import os
import subprocess
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from pathlib import Path

PUBLISHED_WHEEL_URL = (
    "https://github.com/superdurable/dex/releases/download/agents%2Fv{version}/"
    "{distribution}-{version}-py3-none-any.whl"
)
_BUILD_TIMEOUT_SECONDS = 1800
_LOG_TAIL_CHARACTERS = 3000


class PackageSource(enum.Enum):
    """Where the commands and the agent libraries come from."""

    LOCAL = "local"
    PUBLISHED = "published"


class BuildFailure(Exception):
    """A build or install command failed; the message holds its log tail."""


@dataclass
class Toolchain:
    """The tools and directories every build and run uses.

    Attributes:
        checkout: The repository checkout that local builds use.
        work_dir: Where builds, homes, and logs go.
        dexcli: The dexcli executable, also passed to the commands as ``DEXCLI``.
        node_bin_dir: A directory put first on ``PATH`` for Node.js, or ``None``.
        python_version: The Python version of every virtual environment.
        source: Local builds or published packages.
        published_version: The release version for published packages.
    """

    checkout: Path
    work_dir: Path
    dexcli: Path
    node_bin_dir: Path | None
    python_version: str
    source: PackageSource
    published_version: str
    built_directories: set[Path] = field(default_factory=set)

    def search_path(self) -> str:
        """Return ``PATH`` with the Node.js directory first."""
        path = os.environ.get("PATH", "")
        return f"{self.node_bin_dir}{os.pathsep}{path}" if self.node_bin_dir else path

    def build_environment(self, extra: Mapping[str, str] | None = None) -> dict[str, str]:
        """Return the host environment for builds, with the Node.js directory on ``PATH``."""
        environment = dict(os.environ)
        environment["PATH"] = self.search_path()
        environment.update(extra or {})
        return environment

    def run_logged(
        self,
        argv: Sequence[str],
        cwd: Path,
        log_name: str,
        environment: Mapping[str, str] | None = None,
    ) -> None:
        """Run a build command, appending its output to ``<work_dir>/logs/<log_name>.log``."""
        log_path = self.work_dir / "logs" / f"{log_name}.log"
        log_path.parent.mkdir(parents=True, exist_ok=True)
        with log_path.open("a", encoding="utf-8") as log:
            log.write(f"\n$ (cd {cwd} && {' '.join(argv)})\n")
            log.flush()
            completed = subprocess.run(
                list(argv),
                cwd=cwd,
                env=dict(environment) if environment is not None else self.build_environment(),
                stdout=log,
                stderr=subprocess.STDOUT,
                timeout=_BUILD_TIMEOUT_SECONDS,
                check=False,
            )
        if completed.returncode != 0:
            tail = log_path.read_text(encoding="utf-8", errors="replace")[-_LOG_TAIL_CHARACTERS:]
            raise BuildFailure(
                f"{' '.join(argv)} exited with {completed.returncode} in {cwd}; "
                f"log {log_path}:\n{tail}"
            )

    def build_python_command(self, package_dir_name: str, command: str) -> list[str]:
        """Build or install one Python command and return its argv prefix."""
        venv = self.work_dir / "build" / self.source.value / f"python-{package_dir_name}"
        package_dir = self.checkout / "agents" / "cli" / "python" / package_dir_name
        if self.source is PackageSource.LOCAL:
            self.run_logged(
                ["uv", "sync", "--locked", "--no-dev", "--python", self.python_version],
                package_dir,
                f"build-python-{package_dir_name}",
                self.build_environment({"UV_PROJECT_ENVIRONMENT": str(venv)}),
            )
        else:
            distribution = f"dex_agents_{package_dir_name.replace('-', '_')}_cli"
            wheel_url = PUBLISHED_WHEEL_URL.format(
                version=self.published_version, distribution=distribution
            )
            self.run_logged(
                ["uv", "venv", "--allow-existing", "--python", self.python_version, str(venv)],
                self.work_dir,
                f"install-python-{package_dir_name}",
            )
            self.run_logged(
                ["uv", "pip", "install", "--python", str(venv / "bin" / "python"), wheel_url],
                self.work_dir,
                f"install-python-{package_dir_name}",
            )
        return [str(venv / "bin" / command)]

    def build_typescript_command(
        self, package_dir_name: str, adapter_dir_name: str | None, command: str
    ) -> list[str]:
        """Build or install one TypeScript command and return its argv prefix."""
        if self.source is PackageSource.PUBLISHED:
            prefix = self.work_dir / "build" / "published" / f"typescript-{package_dir_name}"
            prefix.mkdir(parents=True, exist_ok=True)
            package = f"@superdurable/dex-agents-{package_dir_name}-cli@{self.published_version}"
            self.run_logged(
                ["npm", "install", "--prefix", str(prefix), "--no-fund", "--no-audit", package],
                prefix,
                f"install-typescript-{package_dir_name}",
            )
            return [str(prefix / "node_modules" / ".bin" / command)]
        agents_dir = self.checkout / "agents"
        directories = [agents_dir / "typescript" / "core"]
        if adapter_dir_name is not None:
            directories.append(agents_dir / "typescript" / adapter_dir_name)
        directories.append(agents_dir / "cli" / "typescript" / "core")
        command_dir = agents_dir / "cli" / "typescript" / package_dir_name
        directories.append(command_dir)
        for directory in directories:
            self.build_npm_package(directory)
        return ["node", str(command_dir / "dist" / "src" / "bin.js")]

    def build_npm_package(self, directory: Path) -> None:
        """Run ``npm ci`` and ``npm run build`` in a checkout directory once per run."""
        if directory in self.built_directories:
            return
        log_name = "build-npm-" + "-".join(directory.relative_to(self.checkout).parts)
        self.run_logged(["npm", "ci", "--no-fund", "--no-audit"], directory, log_name)
        self.run_logged(["npm", "run", "build"], directory, log_name)
        self.built_directories.add(directory)
