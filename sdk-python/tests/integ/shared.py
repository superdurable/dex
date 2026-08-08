# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dataclasses import dataclass
from uuid import uuid4

from dex import Context, Step, StepDecision, graceful_complete


@dataclass
class ModelInput:
    value: int = 0


class CompleteStringStep(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        del context
        return graceful_complete(input)


def unique_id(prefix: str) -> str:
    return f"{prefix}-{uuid4()}"
