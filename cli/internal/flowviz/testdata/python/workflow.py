# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from typing import final
from dex import Attribute, Flow, Step, StepDecision, StepList, Stream
from catalog_connector import create_lookup as lookup_record, create_trigger

RESULT = Attribute("LookupResult", dict)
PROGRESS = Stream("LookupProgress", dict)
TEXT = Stream("LookupText", str)
CONNECTION_NAME = "primary"


# dex:group group-id:lookup group-label:"Lookup"
# dex:explanation text:"Complete the Flow after the lookup."
@final
class FinishStep(Step):
    def execute(self, context, step_input):
        return StepDecision.force_complete()


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

    def get_connector_trigger_bindings(self):
        return [create_trigger(connection_name=CONNECTION_NAME, binding_name="updates")]
