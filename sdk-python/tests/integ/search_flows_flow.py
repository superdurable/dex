# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dex import (
    Attribute,
    AttributeIndex,
    Context,
    Flow,
    IndexType,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    graceful_complete,
)

KEYWORD_KEY = "CustomKeywordField"


class IndexStep(Step[str]):
    def __init__(self, flow: "SearchFlowsFlow") -> None:
        self.flow = flow

    def execute(self, context: Context, input: str) -> StepDecision:
        self.flow.keyword.set(context, input)
        return graceful_complete(input)


class SearchFlowsFlow(Flow[str]):
    def __init__(self) -> None:
        self.keyword = Attribute(
            KEYWORD_KEY,
            str,
            AttributeIndex(IndexType.KEYWORD),
        )
        self.index = IndexStep(self)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.index)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.keyword)
