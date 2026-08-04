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
from dex.tests.workflows.java_duplicate_rpc_memo_workflow import (
    JavaDuplicateRpcMemoWorkflow,
    RpcMemoWorkflowState2,
    TEST_DATA_OBJECT_KEY,
    TEST_SEARCH_ATTRIBUTE_INT,
    TEST_SEARCH_ATTRIBUTE_KEY,
    RPC_INPUT,
    RPC_OUTPUT,
    TEST_STR,
    TEST_DELAY,
)

class TestRpcWithMemo(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = Client(registry)

    def test_rpc_memo_workflow_func1(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"
        run_id = self.client.start_workflow(
            JavaDuplicateRpcMemoWorkflow, wf_id, 30, 999
        )

        self.client.invoke_rpc(
            wf_id, JavaDuplicateRpcMemoWorkflow.test_rpc_set_data_attribute, TEST_STR
        )
        value = self.client.invoke_rpc(
            wf_id,
            JavaDuplicateRpcMemoWorkflow.test_rpc_get_data_attribute_strong_consistency,
        )
        assert value == TEST_STR
        time.sleep(TEST_DELAY)
        value = self.client.invoke_rpc(
            wf_id, JavaDuplicateRpcMemoWorkflow.test_rpc_get_data_attribute, None, str
        )

        self.client.invoke_rpc(
            wf_id, JavaDuplicateRpcMemoWorkflow.test_rpc_set_data_attribute, None
        )
        value = self.client.invoke_rpc(
            wf_id,
            JavaDuplicateRpcMemoWorkflow.test_rpc_get_data_attribute_strong_consistency,
        )
        assert value is None
        time.sleep(TEST_DELAY)
        value = self.client.invoke_rpc(
            wf_id, JavaDuplicateRpcMemoWorkflow.test_rpc_get_data_attribute
        )
        assert value is None

        self.client.invoke_rpc(
            wf_id, JavaDuplicateRpcMemoWorkflow.test_rpc_set_keyword, TEST_STR
        )
        value = self.client.invoke_rpc(
            wf_id, JavaDuplicateRpcMemoWorkflow.test_rpc_get_keyword_strong_consistency
        )
        assert value == TEST_STR
        time.sleep(TEST_DELAY)
        value = self.client.invoke_rpc(
            wf_id, JavaDuplicateRpcMemoWorkflow.test_rpc_get_keyword
        )
        assert value == TEST_STR

        self.client.invoke_rpc(
            wf_id, JavaDuplicateRpcMemoWorkflow.test_rpc_set_keyword, None
        )
        value = self.client.invoke_rpc(
            wf_id, JavaDuplicateRpcMemoWorkflow.test_rpc_get_keyword_strong_consistency
        )
        assert value is None
        time.sleep(TEST_DELAY)
        value = self.client.invoke_rpc(
            wf_id, JavaDuplicateRpcMemoWorkflow.test_rpc_get_keyword
        )
        assert value is None

        rpc_output = self.client.invoke_rpc(
            wf_id, JavaDuplicateRpcMemoWorkflow.test_rpc_func1, RPC_INPUT
        )
        assert RPC_OUTPUT == rpc_output

        output = self.client.wait_for_workflow_completion(wf_id, int)
        RpcMemoWorkflowState2.reset_counter()
        assert 2 == output

        data_attributes = self.client.get_workflow_data_attributes(
            JavaDuplicateRpcMemoWorkflow, wf_id, run_id, [TEST_DATA_OBJECT_KEY]
        )
        assert TEST_DATA_OBJECT_KEY in data_attributes
        assert data_attributes[TEST_DATA_OBJECT_KEY] == RPC_INPUT

        search_attributes = self.client.get_workflow_search_attributes(
            JavaDuplicateRpcMemoWorkflow,
            wf_id,
            [TEST_SEARCH_ATTRIBUTE_KEY, TEST_SEARCH_ATTRIBUTE_INT],
            run_id,
        )
        assert TEST_SEARCH_ATTRIBUTE_INT in search_attributes
        assert TEST_SEARCH_ATTRIBUTE_KEY in search_attributes
        assert search_attributes[TEST_SEARCH_ATTRIBUTE_INT] == RPC_OUTPUT
        assert search_attributes[TEST_SEARCH_ATTRIBUTE_KEY] == RPC_INPUT
