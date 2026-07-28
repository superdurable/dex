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
from dex.tests.workflows.wait_signal_workflow import (
    WaitSignalWorkflow,
    test_channel_int,
    test_channel_str,
    test_channel_none,
    test_channel1,
    test_idle_channel_none,
)


class TestSignal(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = Client(registry)

    def test_signal(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"
        self.client.start_workflow(WaitSignalWorkflow, wf_id, 10)
        self.client.signal_workflow(wf_id, test_channel_int, 123)
        self.client.signal_workflow(wf_id, test_channel_str, "abc")
        self.client.signal_workflow(wf_id, test_channel_none)

        self.client.signal_workflow(wf_id, test_channel1)
        res = self.client.wait_for_workflow_completion(wf_id, str)
        assert res == "abc"

    def test_signal_channel_size(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"
        self.client.start_workflow(WaitSignalWorkflow, wf_id, 1)
        self.client.signal_workflow(wf_id, test_idle_channel_none)
        self.client.signal_workflow(wf_id, test_idle_channel_none)
        self.client.signal_workflow(wf_id, test_idle_channel_none)
        res = self.client.invoke_rpc(
            wf_id, WaitSignalWorkflow.get_idle_signal_channel_size
        )
        assert res == 3
