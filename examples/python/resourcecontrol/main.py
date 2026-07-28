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

from random import randint
from flask import Flask, request
from iwf.iwf_api.models import (
    WorkflowStateExecuteRequest,
    WorkflowStateWaitUntilRequest,
    WorkflowWorkerRpcRequest,
)
from iwf.worker_service import (
    WorkerService,
)
from iwf.workflow_options import WorkflowOptions

from iwf_config import client, worker_service
from controller_workflow import (
    ControllerWorkflow,
    SPOT_INSTANCE_IDS,
    Request, DA_INSTANCE_ID
)
from iwf.errors import WorkflowNotExistsError
from processing_workflow import ProcessingWorkflow

flask_app = Flask(__name__)


# http://localhost:8802/controller/request?id=123
@flask_app.route("/controller/request")
def start_request():
    id = request.args["id"]
    req = Request(id=id, data="abcd")

    # for extension, instead of randomly picking, we could sort the list based on usage, as you described in the advanced use case
    rand_idx = randint(0, len(SPOT_INSTANCE_IDS)-1)
    instance_id = SPOT_INSTANCE_IDS[rand_idx]

    print(f"call some API here to check if the instance is available, if not then pick another one from the list")

    controller_workflow_id = f"controller_workflow_{instance_id}"
    try:
        success = client.invoke_rpc(controller_workflow_id, ControllerWorkflow.enqueue, req)
    except WorkflowNotExistsError:
        client.start_workflow(ControllerWorkflow, controller_workflow_id, 0, req,
                                  WorkflowOptions(
                                      initial_data_attributes={DA_INSTANCE_ID: instance_id},
                                  )
                              )
        success = True
    if success:    
        return "request is accepted"
    else:
        # for extension, move this route logic into a RequestWorkflow, with single state to have backoff retry
        # so that the request is always accepted instead of denying
        return "request is denied because instance is busy. Please retry later"


# http://localhost:8802/controller/shutdown?instance_id=permanentID1
@flask_app.route("/controller/shutdown")
def shutdown_instance():
    instance_id = request.args["instance_id"]
    controller_workflow_id = f"controller_workflow_{instance_id}"
    client.invoke_rpc(controller_workflow_id, ControllerWorkflow.shutdown, None)
    return "done"

# http://localhost:8802/controller/processing/describe?id=123
@flask_app.route("/controller/processing/describe")
def describe_request():
    id = request.args["id"]
    child_workflow_id = f"processing-{id}"
    return client.invoke_rpc(child_workflow_id, ProcessingWorkflow.describe)


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
    print(traceback.format_exc())
    return traceback.format_exc(), 500


def main():
    flask_app.run(host="0.0.0.0", port=8802)


if __name__ == "__main__":
    main()
