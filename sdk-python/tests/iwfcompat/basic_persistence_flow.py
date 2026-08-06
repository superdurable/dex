# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from datetime import datetime

from dex import (
    Attribute,
    AttributeIndex,
    AttributeMap,
    Context,
    Flow,
    IndexType,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Wait,
    graceful_complete,
)

from .shared import ModelInput


class PersistenceStep(Step[str]):
    def __init__(self, flow: "BasicPersistenceFlow") -> None:
        self.flow = flow

    def wait_for(self, context: Context, input: str) -> Wait:
        self.flow.data.set(context, input)
        self.flow.data_map.set(context, "one", input)
        context.set_step_execution_local("local", input)
        context.record_event("written", input)
        return Wait.skip_immediately()

    def execute(self, context: Context, input: str) -> StepDecision:
        self.flow.keyword.set(context, input)
        self.flow.integer.set(context, 1)
        self.flow.datetime.set(
            context,
            datetime.fromisoformat("2023-04-17T21:17:49+00:00"),
        )
        self.flow.model.set(context, ModelInput())
        self.flow.data_map.delete(context, "one")
        return graceful_complete(self.flow.data.get(context))


class BasicPersistenceFlow(Flow[str]):
    def __init__(self) -> None:
        self.initial = Attribute("data-obj-0", str)
        self.data = Attribute("data-obj-1", str)
        self.model = Attribute("data-obj-2", ModelInput)
        self.data_map = AttributeMap("data-map", str)
        self.keyword = Attribute(
            "CustomKeywordField",
            str,
            AttributeIndex(IndexType.KEYWORD),
        )
        self.integer = Attribute(
            "CustomIntField",
            int,
            AttributeIndex(IndexType.INT),
        )
        self.datetime = Attribute(
            "CustomDatetimeField",
            datetime,
            AttributeIndex(IndexType.DATETIME),
        )
        self.start = PersistenceStep(self)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.start)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema(
            attributes=(
                self.initial,
                self.data,
                self.model,
                self.data_map,
                self.keyword,
                self.integer,
                self.datetime,
            )
        )
