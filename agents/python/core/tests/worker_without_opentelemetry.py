# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A Worker child that cannot import OpenTelemetry, as without the ``otel`` extra."""

from __future__ import annotations

import os
import sys
from collections.abc import Sequence
from importlib.abc import MetaPathFinder
from importlib.machinery import ModuleSpec
from pathlib import Path
from types import ModuleType

#: Environment variable naming the file that records every blocked import.
BLOCKED_IMPORT_LOG_ENVIRONMENT_VARIABLE = "SCRIPTED_AGENT_BLOCKED_IMPORT_LOG"


class OpenTelemetryImportBlocker(MetaPathFinder):
    def __init__(self, log_path: Path) -> None:
        self._log_path = log_path

    def find_spec(
        self,
        fullname: str,
        path: Sequence[str] | None,
        target: ModuleType | None = None,
    ) -> ModuleSpec | None:
        if fullname != "opentelemetry" and not fullname.startswith("opentelemetry."):
            return None
        with self._log_path.open("a", encoding="utf-8") as log:
            log.write(f"{fullname}\n")
        raise ModuleNotFoundError(f"No module named {fullname!r}", name=fullname)


if __name__ == "__main__":
    sys.meta_path.insert(
        0, OpenTelemetryImportBlocker(Path(os.environ[BLOCKED_IMPORT_LOG_ENVIRONMENT_VARIABLE]))
    )
    from dex_agents.testing import run_agent_worker
    from tests.scripted_agent import build_scripted_agent_flow_from_environment

    run_agent_worker([build_scripted_agent_flow_from_environment()])
