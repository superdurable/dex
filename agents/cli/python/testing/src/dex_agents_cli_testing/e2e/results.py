# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Step results and the summary table."""

from __future__ import annotations

import enum
import json
from collections.abc import Sequence
from dataclasses import dataclass, field
from pathlib import Path


class StepStatus(enum.Enum):
    """The outcome of one scenario step."""

    PASS = "PASS"
    FAIL = "FAIL"
    SKIP = "SKIP"


@dataclass(frozen=True)
class StepResult:
    """One step of one case.

    Attributes:
        case: The command or framework, such as ``py-openai-sdk``.
        step: The step, such as ``a-read``.
        status: The outcome.
        detail: Evidence for a failure or the reason for a skip; short facts for a pass.
    """

    case: str
    step: str
    status: StepStatus
    detail: str = ""


@dataclass
class ResultTable:
    """Every step result of one suite run.

    Attributes:
        title: The suite's name.
        steps: The step names, in column order.
        results: The results, in the order they were recorded.
    """

    title: str
    steps: Sequence[str]
    results: list[StepResult] = field(default_factory=list)

    def record(self, case: str, step: str, status: StepStatus, detail: str = "") -> StepResult:
        """Record one result and print it as it happens."""
        result = StepResult(case, step, status, detail)
        self.results.append(result)
        print(f"[{status.value}] {case} {step}: {detail}"[:2000], flush=True)
        return result

    def has_failure(self) -> bool:
        """Report whether any step failed."""
        return any(result.status is StepStatus.FAIL for result in self.results)

    def render(self) -> str:
        """Return a Markdown table of cases by steps, then the details of every non-pass."""
        cases = list(dict.fromkeys(result.case for result in self.results))
        cells = {(result.case, result.step): result.status.value for result in self.results}
        lines = [
            f"## {self.title}",
            "",
            "| Case | " + " | ".join(self.steps) + " |",
            "| --- |" + " --- |" * len(self.steps),
        ]
        for case in cases:
            row = [cells.get((case, step), "-") for step in self.steps]
            lines.append(f"| {case} | " + " | ".join(row) + " |")
        details = [result for result in self.results if result.status is not StepStatus.PASS]
        if details:
            lines += ["", "### Failures and skips", ""]
            lines += [
                f"- **{result.case} {result.step}** {result.status.value}: {result.detail}"
                for result in details
            ]
        return "\n".join(lines) + "\n"

    def write_json(self, path: Path) -> None:
        """Write every result as JSON."""
        records = [
            {
                "case": result.case,
                "step": result.step,
                "status": result.status.value,
                "detail": result.detail,
            }
            for result in self.results
        ]
        path.write_text(json.dumps(records, indent=2) + "\n", encoding="utf-8")
