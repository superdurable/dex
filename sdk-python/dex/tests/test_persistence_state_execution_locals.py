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
from dex.tests.workflows.persistence_state_execution_local_workflow import (
    PersistenceStateExecutionLocalWorkflow,
    PERSISTENCE_LOCAL_VALUE,
)
from dex.workflow_options import WorkflowOptions

class TestPersistenceExecutionLocalRead(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = Client(registry)

    def test_persistence_execution_local_workflow(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"
        start_options = WorkflowOptions()
        self.client.start_workflow(
            PersistenceStateExecutionLocalWorkflow, wf_id, 200, None, start_options
        )
        self.client.wait_for_workflow_completion(wf_id, None)
        res = self.client.invoke_rpc(
            wf_id, PersistenceStateExecutionLocalWorkflow.test_persistence_read
        )
        assert res == PERSISTENCE_LOCAL_VALUE
