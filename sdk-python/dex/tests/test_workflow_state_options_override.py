# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import inspect
import time
import unittest

from dex.client import Client
from dex.workflow_options import WorkflowOptions

from dex.dex_api.models import IDReusePolicy
from dex.tests.worker_server import registry

from dex.tests.workflows.state_options_override_workflow import (
    StateOptionsOverrideWorkflow,
)


class TestStateOptionsOverrideWorkflow(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = Client(registry)

    def test_override(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"
        self.client.start_workflow(
            StateOptionsOverrideWorkflow,
            wf_id,
            10,
            "input",
            WorkflowOptions(workflow_id_reuse_policy=IDReusePolicy.DISALLOW_REUSE),
        )
        output = self.client.wait_for_workflow_completion(wf_id)

        assert (
            output
            == "input_InitState_waitUntil_completed_InitState_execute_completed_NonInitState_execute_completed"
        )
