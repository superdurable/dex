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
from dex.tests.workflows.recovery_workflow import RecoveryWorkflow

class Test(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = Client(registry)

    def test_workflow_recovery(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"
        self.client.start_workflow(RecoveryWorkflow, wf_id, 10)
        result = self.client.wait_for_workflow_completion(wf_id, str)
        assert result == "done"
