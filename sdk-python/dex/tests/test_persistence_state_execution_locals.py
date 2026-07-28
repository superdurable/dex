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
from dex.tests.worker_server import registry
from dex.tests.workflows.persistence_state_execution_local_workflow import (
    PersistenceStateExecutionLocalWorkflow,
    PERSISTENCE_LOCAL_VALUE,
)
from dex.workflow_options import WorkflowOptions


class TestPersistenceExecutionLocalRead(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = Client(registry)

    def test_persistence_execution_local_workflow(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"
        start_options = WorkflowOptions()
        self.client.start_workflow(
            PersistenceStateExecutionLocalWorkflow, wf_id, 200, None, start_options
        )
        self.client.wait_for_workflow_completion(wf_id, None)
        res = self.client.invoke_rpc(
            wf_id, PersistenceStateExecutionLocalWorkflow.test_persistence_read
        )
        assert res == PERSISTENCE_LOCAL_VALUE
