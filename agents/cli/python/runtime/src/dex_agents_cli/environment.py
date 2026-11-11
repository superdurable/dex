# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The machine settings of a CLI command: paths and the environment variables it reads.

This is the only module that reads environment variables.
"""

from __future__ import annotations

import os
from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path

#: The ``dexcli`` release a command downloads when none is installed.
DEXCLI_VERSION = "1.44.0"
#: Where ``dexcli`` releases are published; each tag is ``cli-v<version>``.
DEXCLI_RELEASE_BASE_URL = "https://github.com/superdurable/dex/releases/download"
#: The ports a running ``dexcli dev`` uses by default.
DEFAULT_DEV_DEX_PORT = 8801
DEFAULT_DEV_WEB_PORT = 8802

#: A ``dexcli`` executable to use instead of searching ``PATH``.
DEXCLI_ENVIRONMENT_VARIABLE = "DEXCLI"
#: The Dex port a command probes for, and passes to, ``dexcli dev``.
DEX_PORT_ENVIRONMENT_VARIABLE = "DEX_AGENTS_DEX_PORT"
#: The Dex Web port a command probes for, and passes to, ``dexcli dev``.
WEB_PORT_ENVIRONMENT_VARIABLE = "DEX_AGENTS_WEB_PORT"
#: A mirror of the ``dexcli`` release downloads.
DEXCLI_DOWNLOAD_URL_ENVIRONMENT_VARIABLE = "DEX_AGENTS_DEXCLI_DOWNLOAD_URL"


@dataclass(frozen=True)
class RuntimeEnvironment:
    """The paths and machine settings one command run uses.

    Attributes:
        home: The home directory; ``~`` in the configuration means it.
        dexcli: The ``dexcli`` executable from ``DEXCLI``, or ``None`` to search.
        dev_dex_port: The Dex port of ``dexcli dev``; 8801 by default.
        dev_web_port: The Dex Web port of ``dexcli dev``; 8802 by default.
        has_custom_dev_ports: Whether either port came from the environment, so
            a started ``dexcli dev`` must use exactly these ports.
        dexcli_version: The ``dexcli`` release to download.
        dexcli_release_base_url: Base URL of the release downloads.
        search_path: The ``PATH`` directories, searched for ``dexcli``.
    """

    home: Path
    dexcli: Path | None = None
    dev_dex_port: int = DEFAULT_DEV_DEX_PORT
    dev_web_port: int = DEFAULT_DEV_WEB_PORT
    has_custom_dev_ports: bool = False
    dexcli_version: str = DEXCLI_VERSION
    dexcli_release_base_url: str = DEXCLI_RELEASE_BASE_URL
    search_path: tuple[Path, ...] = ()

    @classmethod
    def from_environment(cls, environ: Mapping[str, str] | None = None) -> RuntimeEnvironment:
        """Read the settings from environment variables.

        Args:
            environ: The variables; ``None`` reads ``os.environ``.

        Returns:
            The settings.

        Raises:
            ValueError: If a port variable is not a port number.
        """
        values = os.environ if environ is None else environ
        dexcli = values.get(DEXCLI_ENVIRONMENT_VARIABLE)
        dex_port = values.get(DEX_PORT_ENVIRONMENT_VARIABLE)
        web_port = values.get(WEB_PORT_ENVIRONMENT_VARIABLE)
        return cls(
            home=Path(values.get("HOME") or Path.home()),
            dexcli=Path(dexcli) if dexcli else None,
            dev_dex_port=_parse_port(dex_port, DEFAULT_DEV_DEX_PORT),
            dev_web_port=_parse_port(web_port, DEFAULT_DEV_WEB_PORT),
            has_custom_dev_ports=bool(dex_port or web_port),
            dexcli_release_base_url=values.get(DEXCLI_DOWNLOAD_URL_ENVIRONMENT_VARIABLE)
            or DEXCLI_RELEASE_BASE_URL,
            search_path=tuple(
                Path(entry) for entry in values.get("PATH", "").split(os.pathsep) if entry
            ),
        )

    @property
    def dex_directory(self) -> Path:
        """Return ``~/.dex``, which ``dexcli dev`` also uses."""
        return self.home / ".dex"

    @property
    def agents_directory(self) -> Path:
        """Return ``~/.dex/agents``: configuration files, logs, and the server state file."""
        return self.dex_directory / "agents"

    @property
    def bin_directory(self) -> Path:
        """Return ``~/.dex/bin``, where a downloaded ``dexcli`` lives."""
        return self.dex_directory / "bin"

    @property
    def dev_server_state_path(self) -> Path:
        """Return the file that records the ``dexcli dev`` a command started."""
        return self.agents_directory / "dexcli-dev.json"

    def expand_path(self, text: str, relative_to: Path) -> Path:
        """Expand a configuration path: a leading ``~`` is home, a relative path is relative.

        Args:
            text: The path as written.
            relative_to: The directory of the configuration file.

        Returns:
            The absolute path.
        """
        if text == "~" or text.startswith("~/"):
            return self.home / text[2:]
        path = Path(text)
        return path if path.is_absolute() else relative_to / path


def _parse_port(text: str | None, default: int) -> int:
    if not text:
        return default
    port = int(text)
    if not 0 < port <= 65535:
        raise ValueError(f"{text} is not a port number")
    return port
