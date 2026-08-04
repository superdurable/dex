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
from dex.errors import WorkflowAlreadyStartedError
from dex.dex_api.models import WorkflowAlreadyStartedOptions
from dex.tests.worker_server import registry
from dex.tests.workflows.basic_workflow import BasicWorkflow
from dex.workflow_options import WorkflowOptions

class TestWorkflowErrors(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = Client(registry)

    def test_basic_workflow(self):
        original_request_id = "1"
        later_request_id = "2"

        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"

        workflow_already_started_options_1 = WorkflowAlreadyStartedOptions(
            ignore_already_started_error=True
        )
        workflow_already_started_options_1.request_id = original_request_id

        start_options_1 = WorkflowOptions()
        start_options_1.workflow_already_started_options = (
            workflow_already_started_options_1
        )

        wf_run_id = self.client.start_workflow(
            BasicWorkflow, wf_id, 100, "input", start_options_1
        )
        assert wf_run_id

        wf_run_id = self.client.start_workflow(
            BasicWorkflow, wf_id, 100, "input", start_options_1
        )
        assert wf_run_id

        workflow_already_started_options_2 = WorkflowAlreadyStartedOptions(
            ignore_already_started_error=True
        )
        workflow_already_started_options_2.request_id = later_request_id

        start_options_2 = WorkflowOptions()
        start_options_2.workflow_already_started_option = (
            workflow_already_started_options_2
        )

        with self.assertRaises(WorkflowAlreadyStartedError):
            wf_run_id = self.client.start_workflow(
                BasicWorkflow, wf_id, 100, "input", start_options_2
            )
            assert wf_run_id

        res = self.client.wait_for_workflow_completion(wf_id, str)
        assert res == "done"
