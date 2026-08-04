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
from dex.dex_api.models import IDReusePolicy
from dex.dex_api.models import (
    PersistenceLoadingPolicy,
    PersistenceLoadingType,
    WorkflowStateOptions as IdlWorkflowStateOptions,
    RetryPolicy,
    WaitUntilApiFailurePolicy,
)
from dex.tests.worker_server import registry
from dex.tests.workflows.state_options_workflow import (
    StateOptionsWorkflow1,
    StateOptionsWorkflow2,
)
from dex.workflow_options import WorkflowOptions
from dex.workflow_state_options import WorkflowStateOptions, _to_idl_state_options
from ..errors import WorkflowFailed

class TestWorkflowStateOptions(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = Client(registry)

    def test_convert_to_idl(self):
        empty_idl = IdlWorkflowStateOptions()
        assert empty_idl == _to_idl_state_options(False, None, {})

        non_empty = WorkflowStateOptions(
            "state-id",
            search_attributes_loading_policy=PersistenceLoadingPolicy(
                persistence_loading_type=PersistenceLoadingType.LOAD_ALL_WITHOUT_LOCKING
            ),
        )
        non_empty_idl = IdlWorkflowStateOptions(
            skip_wait_until=True,
            search_attributes_loading_policy=PersistenceLoadingPolicy(
                persistence_loading_type=PersistenceLoadingType.LOAD_ALL_WITHOUT_LOCKING
            ),
        )
        assert non_empty_idl == _to_idl_state_options(True, non_empty, {})
        non_empty.state_id = "state-id-2"
        assert non_empty.state_id == "state-id-2"

    """Test that proceed_to_execute_when_wait_until_retry_exhausted correctly handles both enum values."""

    def test_proceed_to_execute_when_wait_until_retry_exhausted(self):
        retry_policy = RetryPolicy(maximum_attempts=1)

        # Test PROCEED_ON_FAILURE
        options_proceed = WorkflowStateOptions(
            proceed_to_execute_when_wait_until_retry_exhausted=WaitUntilApiFailurePolicy.PROCEED_ON_FAILURE,
            wait_until_api_retry_policy=retry_policy,
        )
        result_proceed = _to_idl_state_options(False, options_proceed, {})
        assert (
            result_proceed.wait_until_api_failure_policy
            == WaitUntilApiFailurePolicy.PROCEED_ON_FAILURE
        )

        # Test FAIL_WORKFLOW_ON_FAILURE
        options_fail = WorkflowStateOptions(
            proceed_to_execute_when_wait_until_retry_exhausted=WaitUntilApiFailurePolicy.FAIL_WORKFLOW_ON_FAILURE,
            wait_until_api_retry_policy=retry_policy,
        )
        result_fail = _to_idl_state_options(False, options_fail, {})
        assert (
            result_fail.wait_until_api_failure_policy
            == WaitUntilApiFailurePolicy.FAIL_WORKFLOW_ON_FAILURE
        )

        # Test with None/unset value
        options = WorkflowStateOptions()
        result = _to_idl_state_options(False, options, {})
        # By default, wait_until_api_failure_policy should not be set when proceed_to_execute_when_wait_until_retry_exhausted is None
        # The DEX service will use FAIL_WORKFLOW_ON_FAILURE by default
        from dex.dex_api.types import Unset

        self.assertTrue(isinstance(result.wait_until_api_failure_policy, Unset))

    def test_proceed_on_failure(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"
        self.client.start_workflow(
            StateOptionsWorkflow1,
            wf_id,
            10,
            "input",
            WorkflowOptions(workflow_id_reuse_policy=IDReusePolicy.DISALLOW_REUSE),
        )
        output = self.client.wait_for_workflow_completion(wf_id)

        assert output == "InitState1_execute_completed"

    def test_fail_workflow_on_failure(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"
        self.client.start_workflow(
            StateOptionsWorkflow2,
            wf_id,
            10,
            "input",
            WorkflowOptions(workflow_id_reuse_policy=IDReusePolicy.DISALLOW_REUSE),
        )

        with self.assertRaises(WorkflowFailed):
            self.client.wait_for_workflow_completion(wf_id, str)
