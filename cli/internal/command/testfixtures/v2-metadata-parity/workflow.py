# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from dataclasses import dataclass
from datetime import datetime as DateTime
from enum import Enum
from http import HTTPMethod
from typing import Literal, Optional, Text as ExternalLabel, TypedDict

from dex import Attribute, AttributeIndex, AttributeMap, Channel, Context, Flow, IndexType, PersistenceSchema, RPCResult, Step, StepDecision, StepList, force_complete, rpc

# dex:indexed-attribute attribute-key:state index-key:state index-type:keyword value-type:string description:"Current state"
STATE = Attribute(name="state", value_type=str, index=AttributeIndex(IndexType.KEYWORD, index_key=""))
# dex:indexed-attribute attribute-key:total index-key:case-total index-type:int value-type:int64 description:"Total cases"
TOTAL = Attribute("total", int, AttributeIndex(IndexType.INT, index_key="case-total"))
# dex:indexed-attribute attribute-key:due index-key:due index-type:datetime value-type:datetime description:"Due date"
DUE = Attribute("due", DateTime, AttributeIndex(IndexType.DATETIME))
# dex:indexed-attribute attribute-key:external-label index-key:external-label index-type:keyword value-type:string description:"External label"
EXTERNAL_LABEL = Attribute("external-label", ExternalLabel, AttributeIndex(IndexType.KEYWORD))
LABEL = Attribute("label", str)
LABELS = AttributeMap("labels", str)
PAYLOAD = Attribute("payload", bytes)
EVENTS = Channel("events", str)


class Priority(str, Enum):
    Normal = "Normal"
    Urgent = "Urgent"


class SchedulingDetails(TypedDict):
    due: DateTime


@dataclass
class SchedulingOptions:
    due: DateTime


DETAILS = Attribute("details", SchedulingDetails)
OPTIONS = Attribute("options", SchedulingOptions)
PRIORITY = Attribute("priority", Priority)
EXTERNAL_PRIORITY = Attribute("external-priority", HTTPMethod)


@dataclass(kw_only=True)
class MetadataInput:
    priority: Priority
    note: Optional[str] = None
    tags: tuple[str, ...] | None
    details: dict[str, SchedulingDetails] | None
    mode: Literal["Preview", "Apply"]


# dex:group group-id:finish group-label:"Finish"
# dex:explanation text:"Finish the Flow."
class FinishMetadataStep(Step[MetadataInput]):
    def execute(self, context: Context, step_input: MetadataInput) -> StepDecision:
        return force_complete()


class MetadataFlow(Flow[MetadataInput]):
    def get_steps(self):
        return StepList.start_step(FinishMetadataStep())

    def get_persistence_schema(self):
        return PersistenceSchema.of(STATE, TOTAL, DUE, EXTERNAL_LABEL, LABEL, LABELS, DETAILS, OPTIONS, PRIORITY, EXTERNAL_PRIORITY, PAYLOAD, EVENTS)

    # dex:field attribute-key:label value-type:string editable:false description:"Case label"
    # dex:field attribute-key:labels value-type:attribute-map editable:false description:"Labels by case"
    # dex:field attribute-key:details value-type:json editable:false description:"Scheduling details"
    # dex:field attribute-key:options value-type:json editable:false description:"Scheduling options"
    # dex:field attribute-key:priority value-type:json editable:false description:"Priority"
    # dex:field attribute-key:external-priority value-type:json editable:false description:"External priority"
    # dex:field attribute-key:payload value-type:array editable:false description:"Case payload"
    @rpc(name="GetDexSummary", load_attribute_maps=[LABELS])
    def get_dex_summary(self, context: Context) -> RPCResult[dict[str, object]]:
        TOTAL.get(context)
        DUE.get(context)
        EXTERNAL_LABEL.get(context)
        EVENTS.size(context)
        return RPCResult({
            "label": LABEL.get(context), "labels": LABELS.get(context, "case"),
            "details": DETAILS.get(context), "options": OPTIONS.get(context),
            "priority": PRIORITY.get(context), "external-priority": EXTERNAL_PRIORITY.get(context),
            "payload": PAYLOAD.get(context),
        })

    # dex:field attribute-key:state value-type:string editable:false description:"Current state" ui-slot:status
    @rpc(name="GetDexDisplay")
    def get_dex_display(self, context: Context) -> RPCResult[dict[str, str]]:
        return RPCResult(display_output(STATE.get(context)))


def display_output(value: str) -> dict[str, str]:
    return {"state": value}
