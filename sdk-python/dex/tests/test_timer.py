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
