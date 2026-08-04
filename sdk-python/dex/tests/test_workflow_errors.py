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
from dex.errors import (
    WorkflowAlreadyStartedError,
    WorkflowCanceled,
    WorkflowFailed,
    WorkflowNotExistsError,
    WorkflowStillRunningError,
    WorkflowTerminated,
    WorkflowTimeout,
)
from dex.dex_api.models import WorkflowStopType
from dex.stop_workflow_options import StopWorkflowOptions
from dex.tests.worker_server import registry
from dex.tests.workflows.wait_internal_channel_workflow import (
    WaitInternalChannelWorkflow,
)

class TestWorkflowErrors(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = Client(registry)

    def test_workflow_timeout(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"
        self.client.start_workflow(WaitInternalChannelWorkflow, wf_id, 1)
        with self.assertRaises(WorkflowTimeout):
            self.client.wait_for_workflow_completion(wf_id, str)
        with self.assertRaises(WorkflowNotExistsError):
            self.client.wait_for_workflow_completion("invalid_id", str)

    def test_workflow_still_running_when_wait(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"
        # client_options = ClientOptions.local_default()
        # client_options.api_timeout = 5
        # TODO using a shorter api timeout will throw a different timeout error, it's better to unify it
        self.client.start_workflow(WaitInternalChannelWorkflow, wf_id, 61)

        with self.assertRaises(WorkflowAlreadyStartedError):
            self.client.start_workflow(WaitInternalChannelWorkflow, wf_id, 61)

        with self.assertRaises(WorkflowStillRunningError):
            self.client.wait_for_workflow_completion(wf_id, str)

    def test_workflow_canceled(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"
        self.client.start_workflow(WaitInternalChannelWorkflow, wf_id, 10)
        self.client.stop_workflow(wf_id)
        with self.assertRaises(WorkflowCanceled):
            self.client.wait_for_workflow_completion(wf_id, str)

    def test_workflow_terminated(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"
        self.client.start_workflow(WaitInternalChannelWorkflow, wf_id, 10)
        self.client.stop_workflow(
            wf_id,
            StopWorkflowOptions(
                workflow_stop_type=WorkflowStopType.TERMINATE, reason="test"
            ),
        )
        with self.assertRaises(WorkflowTerminated):
            self.client.wait_for_workflow_completion(wf_id, str)

    def test_workflow_failed(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"
        self.client.start_workflow(WaitInternalChannelWorkflow, wf_id, 10)
        self.client.stop_workflow(
            wf_id,
            StopWorkflowOptions(
                workflow_stop_type=WorkflowStopType.FAIL, reason="test"
            ),
        )
        with self.assertRaises(WorkflowFailed):
            self.client.wait_for_workflow_completion(wf_id, str)
