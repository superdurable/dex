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

import time
import unittest
from threading import Thread

from flask import Flask, request
from iwf.client import Client
from iwf.iwf_api.models import (
    WorkflowStateExecuteRequest,
    WorkflowStateWaitUntilRequest,
    WorkflowWorkerRpcRequest,
)
from iwf.registry import Registry
from iwf.worker_service import WorkerService

from basic.basic_workflow import BasicWorkflow

_registry = Registry()
_registry.add_workflow(BasicWorkflow())
_worker_service = WorkerService(_registry)
_client = Client(_registry)

_flask_app = Flask(__name__)


@_flask_app.route(WorkerService.api_path_workflow_state_wait_until, methods=["POST"])
def handle_wait_until():
    req = WorkflowStateWaitUntilRequest.from_dict(request.json)
    return _worker_service.handle_workflow_state_wait_until(req).to_dict()


@_flask_app.route(WorkerService.api_path_workflow_state_execute, methods=["POST"])
def handle_execute():
    req = WorkflowStateExecuteRequest.from_dict(request.json)
    return _worker_service.handle_workflow_state_execute(req).to_dict()


@_flask_app.route(WorkerService.api_path_workflow_worker_rpc, methods=["POST"])
def handle_rpc():
    req = WorkflowWorkerRpcRequest.from_dict(request.json)
    return _worker_service.handle_workflow_worker_rpc(req).to_dict()


@_flask_app.errorhandler(Exception)
def internal_error(exception):
    return _worker_service.handle_worker_error(exception), 500


_worker = Thread(target=_flask_app.run, args=("0.0.0.0", 8802), daemon=True)
_worker.start()
# Give Flask a moment to bind the port before tests start workflows.
time.sleep(1)


class TestBasicSample(unittest.TestCase):
    def test_basic_workflow_with_approve(self):
        wf_id = f"samples-basic-{time.time_ns()}"
        run_id = _client.start_workflow(BasicWorkflow, wf_id, 60, 5)
        self.assertTrue(run_id)

        appended = _client.invoke_rpc(wf_id, BasicWorkflow.append_string, "hello")
        self.assertIn("hello", appended)

        _client.invoke_rpc(wf_id, BasicWorkflow.approve)
        result = _client.wait_for_workflow_completion(wf_id, str)
        self.assertEqual(result, "approved")
