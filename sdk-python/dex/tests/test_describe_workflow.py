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
from dex.errors import WorkflowNotExistsError
from dex.dex_api.models import WorkflowStatus
from dex.tests.workflows.describe_workflow import DescribeWorkflow
from dex.tests.worker_server import registry


class TestDescribeWorkflow(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = Client(registry)

    def test_describe_workflow(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"

        self.client.start_workflow(DescribeWorkflow, wf_id, 100)
        workflow_info = self.client.describe_workflow(wf_id)
        assert workflow_info.workflow_status == WorkflowStatus.RUNNING

        # Stop the workflow
        self.client.stop_workflow(wf_id)

    def test_describe_workflow_when_workflow_not_exists(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"

        with self.assertRaises(WorkflowNotExistsError):
            self.client.describe_workflow(wf_id)
