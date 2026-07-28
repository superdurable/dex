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
from dex.tests.workflows.timer_workflow import TimerWorkflow, WaitState


class TestTimer(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = Client(registry)

    def test_timer(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"

        self.client.start_workflow(TimerWorkflow, wf_id, 100, 5)
        time.sleep(1)
        self.client.skip_timer_at_command_index(wf_id, WaitState)
        start_ms = time.time_ns() / 1000000
        self.client.wait_for_workflow_completion(wf_id, None)
        elapsed_ms = time.time_ns() / 1000000 - start_ms
        assert (
            3000 <= elapsed_ms <= 6000
        ), f"expected 5000 ms timer, actual is {elapsed_ms}"
