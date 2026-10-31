# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from dataclasses import dataclass

from dex import (
    Attribute,
    AttributeMap,
    Context,
    Flow,
    PersistenceSchema,
    StartFlowOptions,
    rpc,
)


@dataclass(frozen=True)
class GetAttributesNote:
    text: str
    priority: int


LABEL = Attribute("get-attributes-label", str)
COUNT = Attribute("get-attributes-count", int)
NOTE = Attribute("get-attributes-note", GetAttributesNote)
EMPTY_LABEL = Attribute("get-attributes-empty-label", str)
ZERO_COUNT = Attribute("get-attributes-zero-count", int)
UNSET_COUNT = Attribute("get-attributes-unset-count", int)
QUANTITIES = AttributeMap("get-attributes-quantities", int)
SECRET = Attribute("get-attributes-secret", str, hard_delete_on_null=True)
LARGE_VALUE = Attribute("get-attributes-large-value", str)
CLOSED_RUN_LARGE_VALUE = Attribute("get-attributes-closed-run-large-value", str)

SET_QUANTITY_INSTANCE = "line 1"
UNSET_QUANTITY_INSTANCE = "line-2"
INITIAL_LABEL = "ready"
INITIAL_COUNT = 7
INITIAL_NOTE = GetAttributesNote("ship today", 2)
SECRET_VALUE = "hard-delete-value-" * 16
LARGE_STRING_VALUE = "large-value-" * 64
CLOSED_RUN_LARGE_STRING_VALUE = "closed-run-large-value-" * 32


class GetAttributesFlow(Flow[None]):
    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            LABEL,
            COUNT,
            NOTE,
            EMPTY_LABEL,
            ZERO_COUNT,
            UNSET_COUNT,
            QUANTITIES,
            SECRET,
            LARGE_VALUE,
            CLOSED_RUN_LARGE_VALUE,
        )

    @rpc
    def delete_secret(self, context: Context) -> None:
        SECRET.delete(context)


def get_attributes_start_options() -> StartFlowOptions:
    return (
        StartFlowOptions()
        .with_attribute(LABEL, INITIAL_LABEL)
        .with_attribute(COUNT, INITIAL_COUNT)
        .with_attribute(NOTE, INITIAL_NOTE)
        .with_attribute(EMPTY_LABEL, "")
        .with_attribute(ZERO_COUNT, 0)
        .with_attribute(QUANTITIES, SET_QUANTITY_INSTANCE, 0)
        .with_attribute(SECRET, SECRET_VALUE)
        .with_attribute(LARGE_VALUE, LARGE_STRING_VALUE)
        .with_attribute(CLOSED_RUN_LARGE_VALUE, CLOSED_RUN_LARGE_STRING_VALUE)
    )
