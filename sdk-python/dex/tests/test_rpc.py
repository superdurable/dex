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
from dex.tests.workflows.rpc_workflow import RPCWorkflow, Mydata


class TestRPCs(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = Client(registry)

    def test_simple_rpc(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"
        self.client.start_workflow(RPCWorkflow, wf_id, 10)

        input = Mydata("test", 100)
        output = self.client.invoke_rpc(
            wf_id, RPCWorkflow.test_rpc_input_type, input, Mydata
        )
        assert output == input

        output = self.client.invoke_rpc(wf_id, RPCWorkflow.test_simple_rpc)
        assert output == 123
        wf = RPCWorkflow()
        output = self.client.invoke_rpc(wf_id, wf.test_simple_rpc)
        assert output == 123

    def test_complicated_rpc(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"
        self.client.start_workflow(RPCWorkflow, wf_id, 10)
        self.client.invoke_rpc(wf_id, RPCWorkflow.test_rpc_persistence_write, 100)
        res = self.client.invoke_rpc(wf_id, RPCWorkflow.test_rpc_persistence_read)
        assert res == 100
        self.client.invoke_rpc(wf_id, RPCWorkflow.test_rpc_trigger_state, 200)
        res = self.client.invoke_rpc(wf_id, RPCWorkflow.test_rpc_persistence_read)
        assert res == 200

        res1 = self.client.invoke_rpc(
            wf_id, RPCWorkflow.test_rpc_publish_to_idle_channel, "input-1"
        )
        # Channel Size should be 1
        assert res1 == 1

        res2 = self.client.invoke_rpc(
            wf_id, RPCWorkflow.test_rpc_publish_to_idle_channel, "input-2"
        )
        # Channel Size should be 2
        assert res2 == 2

        self.client.invoke_rpc(wf_id, RPCWorkflow.test_rpc_publish_channel)
        result = self.client.wait_for_workflow_completion(wf_id, str)
        assert result == "done"
