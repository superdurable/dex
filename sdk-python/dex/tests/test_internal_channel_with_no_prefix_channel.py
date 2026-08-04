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
from dex.tests.workflows.internal_channel_workflow_with_no_prefix_channel import (
    InternalChannelWorkflowWithNoPrefixChannel,
    test_non_prefix_channel_name_with_suffix,
)

class TestInternalChannelWithNoPrefix(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = Client(registry)

    def test_internal_channel_workflow_with_no_prefix_channel(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"

        self.client.start_workflow(
            InternalChannelWorkflowWithNoPrefixChannel, wf_id, 5, None
        )

        with self.assertRaises(Exception) as context:
            self.client.wait_for_workflow_completion(wf_id, None)

        self.assertIn("FAILED", context.exception.workflow_status)
        self.assertIn(
            f"WorkerExecutionError: InternalChannel channel_name is not defined {test_non_prefix_channel_name_with_suffix}",
            context.exception.error_message,
        )
