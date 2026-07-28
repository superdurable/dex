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
