# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dataclasses import dataclass

from dex import Context, Step, StepDecision, graceful_complete


@dataclass
class ModelInput:
    value: int = 0


class CompleteStringStep(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        del context
        return graceful_complete(input)
