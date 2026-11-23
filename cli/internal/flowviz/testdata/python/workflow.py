# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from typing import final
from dex import Attribute, AttributeIndex, Flow, IndexType, PersistenceSchema, RPCResult, Step, StepList, Stream, force_complete, rpc
from catalog_connector import create_lookup as lookup_record, create_trigger

RESULT = Attribute("LookupResult", dict)
# dex:indexed-attribute attribute-key:LookupState index-key:keyword1 index-type:keyword value-type:string description:"Lookup state"
STATE = Attribute("LookupState", str, AttributeIndex(IndexType.KEYWORD, "keyword1"))
PROGRESS = Stream("LookupProgress", dict, 65536)
TEXT = Stream("LookupText", str, 65536)
CONNECTION_NAME = "primary"


# dex:group group-id:lookup group-label:"Lookup"
# dex:explanation text:"Complete the Flow after the lookup."
@final
class FinishStep(Step):
    def execute(self, context, step_input):
        return force_complete()


class CatalogFlow(Flow):
    def __init__(self):
        self.finish = FinishStep()
        self.lookup = lookup_record(
            step_type="LookupRecord",
            connection_name=CONNECTION_NAME,
            annotations={"groupId": "lookup", "groupLabel": "Lookup", "explanation": "Look up a catalog record."},
            execute_failure=self.finish,
            found=self.finish,
            unavailable=self.finish,
            result_attribute=RESULT,
            progress_stream=PROGRESS,
            text_stream=TEXT,
        )

    def get_steps(self):
        return StepList.start_step(self.lookup).other_steps(self.finish)

    def get_persistence_schema(self):
        return PersistenceSchema.of(RESULT, STATE, PROGRESS, TEXT)

    # dex:field attribute-key:LookupState value-type:string editable:false description:"Lookup state"
    @rpc(name="GetDexDisplay")
    def display(self, context):
        return RPCResult({"LookupState": STATE.get(context)})

    def get_connector_trigger_bindings(self):
        return [create_trigger(connection_name=CONNECTION_NAME, binding_name="updates")]
