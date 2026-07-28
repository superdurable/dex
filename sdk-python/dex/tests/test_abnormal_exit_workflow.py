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
from dex.errors import WorkflowFailed
from dex.dex_api.models.id_reuse_policy import IDReusePolicy
from dex.tests.worker_server import registry
from dex.tests.workflows.abnormal_exit_workflow import AbnormalExitWorkflow
from dex.tests.workflows.basic_workflow import BasicWorkflow
from dex.workflow_options import WorkflowOptions


class TestAbnormalWorkflow(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = Client(registry)

    def test_abnormal_exit_workflow(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"
        start_options = WorkflowOptions(
            workflow_id_reuse_policy=IDReusePolicy.ALLOW_IF_PREVIOUS_EXITS_ABNORMALLY
        )

        self.client.start_workflow(
            AbnormalExitWorkflow, wf_id, 100, "input", start_options
        )
        with self.assertRaises(WorkflowFailed):
            self.client.wait_for_workflow_completion(wf_id, str)

        # Starting a workflow with the same ID should be allowed since the previous failed abnormally
        self.client.start_workflow(BasicWorkflow, wf_id, 100, "input", start_options)
        res = self.client.wait_for_workflow_completion(wf_id, str)
        assert res == "done"
