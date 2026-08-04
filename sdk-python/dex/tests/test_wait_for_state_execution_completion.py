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
from dex.tests.workflows.wait_for_state_with_wait_for_key_workflow import (
    WaitForStateWithWaitForKeyWorkflow,
    WaitForStateWithWaitForKeyState2,
)
from dex.workflow_options import WorkflowOptions

class TestWaitForStateWithWaitForKey(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = Client(registry)

    def test_wait_for_state_with_wait_for_key_workflow(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"

        wf_opts = WorkflowOptions()
        wf_opts.add_wait_for_completion_state_ids(WaitForStateWithWaitForKeyState2)

        self.client.start_workflow(
            WaitForStateWithWaitForKeyWorkflow, wf_id, 100, None, wf_opts
        )

        self.client.wait_for_state_execution_completion_with_wait_for_key(
            WaitForStateWithWaitForKeyState2, wf_id, "testKey"
        )

        self.client.wait_for_workflow_completion(wf_id)

        # TODO: Improve this test by verifying both workflows completed with describe_workflow method
        # https://github.com/superdurable/dex-python-sdk/issues/22
        #
        # child_wf_id =
        #
        # wf_info = self.client.describe_workflow(wf_id);
        # assert wf_info.workflow_status == WorkflowStatus.COMPLETED
        #
        # child_wf_info = self.client.describe_workflow(child_wf_id);
        # assert child_wf_info.workflow_status == WorkflowStatus.COMPLETED
