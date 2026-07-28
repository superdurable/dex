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

from iwf.workflow import ObjectWorkflow
from iwf.workflow_state import WorkflowState
from iwf.state_schema import StateSchema
from iwf.persistence_schema import PersistenceField, PersistenceSchema
from iwf.state_decision import StateDecision
from iwf.command_request import CommandRequest, TimerCommand
from iwf.command_results import CommandResults
from iwf.persistence import Persistence
from iwf.communication import Communication
from iwf.workflow_context import WorkflowContext
from iwf.rpc import rpc
from controller_workflow import Request
from controller_workflow import ControllerWorkflow
from iwf.errors import WorkflowNotExistsError

from resourcecontrol.controller_workflow import DA_INSTANCE_ID

DA_PARENT_WORKFLOW_ID = "ParentWorkflowId"
DA_PROCESSING_STATUS = "Status"
DA_REQUEST = "Request"

class ProcessingWorkflow(ObjectWorkflow):
    def get_workflow_states(self) -> StateSchema:
        return StateSchema.with_starting_state(
            ValidationStartState(),
            ValidationCompleteState(),
            GpuProcessingStartState(),
            GpuProcessingCompleteState(),
            CompleteState())

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.create(
            PersistenceField.data_attribute_def(DA_PARENT_WORKFLOW_ID, str),
            PersistenceField.data_attribute_def(DA_PROCESSING_STATUS, str),
            PersistenceField.data_attribute_def(DA_INSTANCE_ID, str),
            PersistenceField.data_attribute_def(DA_REQUEST, Request),
        )
    
    @rpc()
    def describe(self, persistence: Persistence)->str:
        return persistence.get_data_attribute(DA_PROCESSING_STATUS)


class ValidationStartState(WorkflowState[Request]):
    def execute(self, ctx: WorkflowContext, req: Request, command_results: CommandResults, persistence: Persistence, communication: Communication) -> StateDecision:
        persistence.set_data_attribute(DA_REQUEST, req) # save it to persistence so that we don't pass it as state input over and over again to other state

        instance_id = persistence.get_data_attribute(DA_INSTANCE_ID)
        print(f"start validation of request {req} in {instance_id} by calling API to the instance/VM endpoint")
        persistence.set_data_attribute(DA_PROCESSING_STATUS, "validation started")

        return StateDecision.single_next_state(ValidationCompleteState)


class ValidationCompleteState(WorkflowState[None]):
    def wait_until(self, ctx: WorkflowContext, ignored: None, persistence: Persistence, communication: Communication) -> CommandRequest:
        return CommandRequest.for_any_command_completed(
            # here use a timer to check the completion after 5 seconds,
            # It can be extended as needed:
            #    1. the timer can change on different iteration by using a data attribute as counter
            #    2. it can wait for a signal from the validation job (may not be possible/easy) or both
            TimerCommand.by_seconds(5)
        )

    def execute(self, ctx: WorkflowContext, ignored: None, command_results: CommandResults, persistence: Persistence, communication: Communication) -> StateDecision:
        instance_id = persistence.get_data_attribute(DA_INSTANCE_ID)
        print(f"completed validation in {instance_id} by calling API to the instance/VM endpoint")
        validation_succ = True

        if validation_succ:
            persistence.set_data_attribute(DA_PROCESSING_STATUS, "validation completed")
            return StateDecision.single_next_state(GpuProcessingStartState)
        else:
            # future extensions: if it can know the instance is not responding anymore, it should call controller workflow to shutdown and move the processing to other instances
            return StateDecision.single_next_state(ValidationCompleteState) # loop back to check again


class GpuProcessingStartState(WorkflowState[None]):
    def execute(self, ctx: WorkflowContext, ignored: None, command_results: CommandResults, persistence: Persistence,
                communication: Communication) -> StateDecision:
        req = persistence.get_data_attribute(DA_REQUEST)
        instance_id = persistence.get_data_attribute(DA_INSTANCE_ID)
        print(f"start processing of request {req} in {instance_id} by calling API to the instance/VM endpoint")
        persistence.set_data_attribute(DA_PROCESSING_STATUS, "processing started")

        return StateDecision.single_next_state(GpuProcessingCompleteState)

class GpuProcessingCompleteState(WorkflowState[None]):
    def wait_until(self, ctx: WorkflowContext, ignored: None, persistence: Persistence, communication: Communication) -> CommandRequest:
        return CommandRequest.for_any_command_completed(
            # here use a timer to check the completion after 5 seconds,
            # It can be extended as needed:
            #    1. the timer can change on different iteration by using a data attribute as counter
            #    2. it can wait for a signal from the validation job (may not be possible/easy) or both
            TimerCommand.by_seconds(5)
        )

    def execute(self, ctx: WorkflowContext, ignored: None, command_results: CommandResults, persistence: Persistence, communication: Communication) -> StateDecision:
        instance_id = persistence.get_data_attribute(DA_INSTANCE_ID)
        print(f"check gpu processing in {instance_id} by calling API to the instance/VM endpoint")

        processing_succ = True
        if processing_succ:
            persistence.set_data_attribute(DA_PROCESSING_STATUS, "gpu processing completed")
            return StateDecision.single_next_state(CompleteState)
        else:
            # future extensions: if it can know the instance is not responding anymore, it should call controller workflow to shutdown and move the processing to other instances
            return StateDecision.single_next_state(GpuProcessingCompleteState) # loop back to check again

class CompleteState(WorkflowState[None]):
    def execute(self, ctx: WorkflowContext, ignored: None, command_results: CommandResults, persistence: Persistence, communication: Communication) -> StateDecision:
        parent_workflow_id = persistence.get_data_attribute(DA_PARENT_WORKFLOW_ID)

        from iwf_config import client
        try:
            client.invoke_rpc(parent_workflow_id, ControllerWorkflow.complete_child_workflow, ctx.workflow_id)
        except WorkflowNotExistsError:
            print("Parent workflow may have completed, possibly a duplicate completion request, ignoring it.")
        
        return StateDecision.graceful_complete_workflow()
