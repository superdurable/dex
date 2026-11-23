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
from typing import Literal, Optional, TypedDict

from dex import Attribute, AttributeIndex, Context, Flow, IndexType, PersistenceSchema, RPCResult, Step, StepDecision, StepList, force_complete, rpc

# dex:indexed-attribute attribute-key:state index-key:state index-type:keyword value-type:string description:"Current state"
STATE = Attribute(name="state", value_type=str, index=AttributeIndex(IndexType.KEYWORD))
LABEL = Attribute("label", str)


class Priority(str, Enum):
    Normal = "Normal"
    Urgent = "Urgent"


class SchedulingDetails(TypedDict):
    due: DateTime


@dataclass(kw_only=True)
class MetadataInput:
    priority: Priority
    note: Optional[str] = None
    tags: list[str] | None
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
        return PersistenceSchema.of(STATE, LABEL)

    # dex:field attribute-key:label value-type:string editable:false description:"Case label"
    @rpc(name="GetDexSummary")
    def get_dex_summary(self, context: Context) -> RPCResult[dict[str, str]]:
        return RPCResult({"label": LABEL.get(context)})

    # dex:field attribute-key:state value-type:string editable:false description:"Current state" ui-slot:status
    @rpc(name="GetDexDisplay")
    def get_dex_display(self, context: Context) -> RPCResult[dict[str, str]]:
        return RPCResult(display_output(STATE.get(context)))


def display_output(value: str) -> dict[str, str]:
    return {"state": value}
