# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

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
