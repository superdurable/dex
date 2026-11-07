# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A ModelSpendLimit that a test steers through files, because Workers are child processes.

``check-mode`` holds ``allow``, ``refuse <reason>``, ``raise``, ``raise-once``, or
``hang``. ``report-mode`` holds ``record``, ``raise``, or ``gate <name>``, which
records, then waits at a FileGate. Each check and report is one JSON line.
"""

from __future__ import annotations

import asyncio
import dataclasses
import json
from pathlib import Path
from typing import Any

from dex_agents import ModelCallCheck, ModelCallDecision, ModelUsageReport
from dex_agents.testing import FileGate

CHECK_MODE_FILE = "check-mode"
REPORT_MODE_FILE = "report-mode"
CHECKS_LOG = "spend-checks.jsonl"
REPORTS_LOG = "spend-reports.jsonl"


class FileSpendLimit:
    def __init__(self, state_dir: Path) -> None:
        self._state_dir = state_dir

    async def check_model_call(self, check: ModelCallCheck) -> ModelCallDecision:
        self._log(CHECKS_LOG, check)
        verb, _, rest = self._mode(CHECK_MODE_FILE, "allow").partition(" ")
        if verb == "refuse":
            return ModelCallDecision.refuse(rest)
        if verb == "raise":
            raise RuntimeError("the spend service is down")
        if verb == "raise-once":
            (self._state_dir / CHECK_MODE_FILE).write_text("allow")
            raise RuntimeError("the spend service blinked")
        if verb == "hang":
            await asyncio.Event().wait()
        return ModelCallDecision.allow()

    async def report_model_usage(self, report: ModelUsageReport) -> None:
        self._log(REPORTS_LOG, report)
        verb, _, rest = self._mode(REPORT_MODE_FILE, "record").partition(" ")
        if verb == "raise":
            raise RuntimeError("the spend service is down")
        if verb == "gate":
            gate = FileGate(self._state_dir, rest)
            gate.mark_reached()
            await gate.wait_for_release()

    def _mode(self, file_name: str, default: str) -> str:
        path = self._state_dir / file_name
        return path.read_text() if path.exists() else default

    def _log(self, file_name: str, entry: Any) -> None:
        with (self._state_dir / file_name).open("a") as log:
            log.write(json.dumps(dataclasses.asdict(entry)) + "\n")


def read_spend_log(state_dir: Path, file_name: str) -> list[dict[str, Any]]:
    path = state_dir / file_name
    if not path.exists():
        return []
    return [json.loads(line) for line in path.read_text().splitlines()]
