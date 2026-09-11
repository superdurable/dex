# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from datetime import datetime

from dex import (
    Attribute,
    AttributeIndex,
    AttributeMap,
    Channel,
    Context,
    Flow,
    IndexType,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Wait,
    graceful_complete,
    rpc,
)

from .shared import ModelInput


class SetAttributesCompleteStep(Step[str]):
    def __init__(self, proceed: Channel[None]) -> None:
        self.proceed = proceed

    def wait_for(self, context: Context, input: str) -> Wait:
        del context, input
        return Wait.until(self.proceed.for_one())

    def execute(self, context: Context, input: str) -> StepDecision:
        del context, input
        return graceful_complete("test-result")


class SetAttributesFlow(Flow[str]):
    def __init__(self) -> None:
        self.data = Attribute("data", str)
        self.data_map = AttributeMap("data-map", str)
        self.model = Attribute("data-model", ModelInput)
        self.keyword = Attribute(
            "CustomKeywordField",
            str,
            AttributeIndex(IndexType.KEYWORD),
        )
        self.text = Attribute(
            "CustomTextField",
            str,
            AttributeIndex(IndexType.FULL_TEXT),
        )
        self.decimal = Attribute(
            "CustomDoubleField",
            float,
            AttributeIndex(IndexType.DOUBLE),
        )
        self.integer = Attribute(
            "CustomIntField",
            int,
            AttributeIndex(IndexType.INT),
        )
        self.bool = Attribute(
            "CustomBoolField",
            bool,
            AttributeIndex(IndexType.BOOL),
        )
        self.keywords = Attribute[tuple[str, ...]](
            "CustomKeywordArrayField",
            tuple[str, ...],
            AttributeIndex(IndexType.KEYWORD_ARRAY),
        )
        self.datetime = Attribute(
            "CustomDatetimeField",
            datetime,
            AttributeIndex(IndexType.DATETIME),
        )
        self.proceed = Channel("proceed", type(None))
        self.start = SetAttributesCompleteStep(self.proceed)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.start)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.data,
            self.data_map,
            self.model,
            self.keyword,
            self.text,
            self.decimal,
            self.integer,
            self.bool,
            self.keywords,
            self.datetime,
            self.proceed,
        )

    @rpc
    def set_indexed(self, context: Context) -> None:
        self.keyword.set(context, "keyword-1")
        self.text.set(context, "text-1")
        self.decimal.set(context, 1.0)
        self.integer.set(context, 1)
        self.bool.set(context, True)
        self.keywords.set(context, ("keyword-1", "keyword-2"))
        self.datetime.set(context, datetime.fromisoformat("2024-11-13T00:00:01.731+00:00"))

    @rpc
    def set_data(self, context: Context, input: str) -> None:
        self.data.set(context, input)

    @rpc
    def set_map_one(self, context: Context, input: str) -> None:
        self.data_map.set(context, "one", input)

    @rpc
    def set_map_special(self, context: Context, input: str) -> None:
        self.data_map.set(context, "special % key", input)

    @rpc
    def set_integer(self, context: Context, input: int) -> None:
        self.integer.set(context, input)

    @rpc
    def set_model(self, context: Context, input: ModelInput) -> None:
        self.model.set(context, input)

    @rpc
    def complete(self, context: Context) -> None:
        self.proceed.publish(context, None)
