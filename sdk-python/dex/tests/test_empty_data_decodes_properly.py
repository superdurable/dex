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

import httpx

from dex.client import Client
from dex.tests.worker_server import registry
from dex.worker_service import WorkerService


class TestBinaryNullDecodesCorrectly(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = Client(registry)

    def test_binary_null_input_decodes_correctly(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"

        response = httpx.post(
            f"http://0.0.0.0:8802/{WorkerService.api_path_workflow_state_execute}",
            json={
                "DataObjects": [
                    {"key": "test-da", "value": {"encoding": "binary/null"}}
                ],
                "commandResults": {
                    "interStateChannelResults": [],
                    "stateStartApiSucceeded": True,
                },
                "context": {
                    "attempt": 1,
                    "firstAttemptTimestamp": 1747935829,
                    "stateExecutionId": "State1-1",
                    "workflowId": wf_id,
                    "workflowRunId": "0196f734-d037-7432-bd63-e1136cd34dbd",
                    "workflowStartedTimestamp": 1747904155,
                },
                "stateInput": {"encoding": "binary/null"},
                "stateLocals": [],
                "workflowStateId": "State1",
                "workflowType": "EmptyDataWorkflow",
            },
        )
        assert response.is_success
        response_json = response.json()
        self.assertEqual(
            response_json,
            {
                "publishToInterStateChannel": [],
                "recordEvents": [],
                "stateDecision": {
                    "nextStates": [
                        {
                            "stateId": "_SYS_GRACEFUL_COMPLETING_WORKFLOW",
                            "stateInput": {
                                "data": '"success"',
                                "encoding": "json/plain",
                            },
                        }
                    ]
                },
                "upsertDataObjects": [],
                "upsertStateLocals": [],
            },
        )
