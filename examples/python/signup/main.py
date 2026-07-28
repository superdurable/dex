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

import traceback

from flask import Flask, request
from iwf.iwf_api.models import (
    WorkflowStateExecuteRequest,
    WorkflowStateWaitUntilRequest,
    WorkflowWorkerRpcRequest,
)
from iwf.worker_service import (
    WorkerService,
)

from signup.iwf_config import client, worker_service
from signup.signup_workflow import UserSignupWorkflow, Form

flask_app = Flask(__name__)


# http://localhost:8802/signup/submit?username=test1&email=abc@c.com
@flask_app.route("/signup/submit")
def signup_submit():
    username = request.args["username"]
    email = request.args["email"]
    form = Form(
        username,
        email,
        request.args.get("firstname", "TestDefaultFirstName"),
        request.args.get("lastname", "TestDefaultLastName"),
    )
    client.start_workflow(UserSignupWorkflow, username, 3600, form)
    return "workflow started"


# http://localhost:8802/signup/verify?username=test1&source=email
@flask_app.route("/signup/verify")
def signup_verify():
    username = request.args["username"]
    source = request.args["source"]
    return client.invoke_rpc(username, UserSignupWorkflow.verify, source)


# http://localhost:8802/signup/describe?username=test1
@flask_app.route("/signup/describe")
def signup_describe():
    username = request.args["username"]
    return client.invoke_rpc(username, UserSignupWorkflow.describe)


@flask_app.route("/")
def index():
    return "iwf workflow home"


# below are iWF workflow worker APIs to be called by iWF server


@flask_app.route(WorkerService.api_path_workflow_state_wait_until, methods=["POST"])
def handle_wait_until():
    req = WorkflowStateWaitUntilRequest.from_dict(request.json)
    resp = worker_service.handle_workflow_state_wait_until(req)
    return resp.to_dict()


@flask_app.route(WorkerService.api_path_workflow_state_execute, methods=["POST"])
def handle_execute():
    req = WorkflowStateExecuteRequest.from_dict(request.json)
    resp = worker_service.handle_workflow_state_execute(req)
    return resp.to_dict()


@flask_app.route(WorkerService.api_path_workflow_worker_rpc, methods=["POST"])
def handle_rpc():
    req = WorkflowWorkerRpcRequest.from_dict(request.json)
    resp = worker_service.handle_workflow_worker_rpc(req)
    return resp.to_dict()


# this handler is extremely useful for debugging iWF
# the WebUI will be able to show you the error with stacktrace
@flask_app.errorhandler(Exception)
def internal_error(exception):
    return traceback.format_exc(), 500


def main():
    flask_app.run(host="0.0.0.0", port=8802)


if __name__ == "__main__":
    main()
