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

from threading import Thread

from flask import Flask, request

from dex.dex_api.models import (
    WorkflowStateExecuteRequest,
    WorkflowStateWaitUntilRequest,
    WorkflowWorkerRpcRequest,
)
from dex.tests import registry
from dex.worker_service import (
    WorkerService,
)

# NOTE: set this to true when debugging(using breakpoints)
# so that it keep the thread running so that we can see the error in history
debug_mode: bool = False

_flask_app = Flask(__name__)

_worker_service = WorkerService(registry)


@_flask_app.route("/")
def index():
    return "hello"


@_flask_app.route(WorkerService.api_path_workflow_state_wait_until, methods=["POST"])
def handle_wait_until():
    req = WorkflowStateWaitUntilRequest.from_dict(request.json)
    resp = _worker_service.handle_workflow_state_wait_until(req)
    return resp.to_dict()


@_flask_app.route(WorkerService.api_path_workflow_state_execute, methods=["POST"])
def handle_execute():
    req = WorkflowStateExecuteRequest.from_dict(request.json)
    resp = _worker_service.handle_workflow_state_execute(req)
    return resp.to_dict()


@_flask_app.route(WorkerService.api_path_workflow_worker_rpc, methods=["POST"])
def handle_rpc():
    req = WorkflowWorkerRpcRequest.from_dict(request.json)
    resp = _worker_service.handle_workflow_worker_rpc(req)
    return resp.to_dict()


# this handler is extremely useful for debugging Dex
# the WebUI will be able to show you the error with stacktrace
@_flask_app.errorhandler(Exception)
def internal_error(exception):
    return _worker_service.handle_worker_error(exception), 500


_webApp = Thread(target=_flask_app.run, args=("0.0.0.0", 8802))
# when debugging, keep the thread running so that we can see the error in history
_webApp.setDaemon(not debug_mode)
_webApp.start()
