# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from datetime import datetime

from dex import (
    Attribute,
    AttributeIndex,
    AttributeMap,
    Flow,
    IndexType,
    PersistenceSchema,
    StepDef,
)

from .shared import CompleteStringStep


class SetAttributesFlow(Flow[str]):
    def __init__(self) -> None:
        self.data = Attribute("data", str)
        self.data_map = AttributeMap("data-map", str)
        self.keyword = Attribute(
            "keyword",
            str,
            AttributeIndex(IndexType.KEYWORD),
        )
        self.text = Attribute(
            "text",
            str,
            AttributeIndex(IndexType.FULL_TEXT),
        )
        self.decimal = Attribute(
            "double",
            float,
            AttributeIndex(IndexType.DOUBLE),
        )
        self.integer = Attribute(
            "int",
            int,
            AttributeIndex(IndexType.INT),
        )
        self.bool = Attribute(
            "bool",
            bool,
            AttributeIndex(IndexType.BOOL),
        )
        self.keywords = Attribute[tuple[str, ...]](
            "keywords",
            tuple,
            AttributeIndex(IndexType.KEYWORD_ARRAY),
        )
        self.datetime = Attribute(
            "datetime",
            datetime,
            AttributeIndex(IndexType.DATETIME),
        )
        self.start = CompleteStringStep()

    def get_steps(self) -> tuple[StepDef, ...]:
        return (StepDef.start_step(self.start),)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema(
            attributes=(
                self.data,
                self.data_map,
                self.keyword,
                self.text,
                self.decimal,
                self.integer,
                self.bool,
                self.keywords,
                self.datetime,
            )
        )
