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

from dataclasses import dataclass

from iwf.command_request import CommandRequest, InternalChannelCommand, TimerCommand
from iwf.command_results import CommandResults
from iwf.communication import Communication
from iwf.communication_schema import CommunicationSchema, CommunicationMethod
from iwf.iwf_api.models import ChannelRequestStatus, PersistenceLoadingPolicy, PersistenceLoadingType
from iwf.persistence import Persistence
from iwf.persistence_schema import PersistenceField, PersistenceSchema
from iwf.rpc import rpc
from iwf.state_decision import StateDecision
from iwf.state_schema import StateSchema
from iwf.workflow import ObjectWorkflow
from iwf.workflow_context import WorkflowContext
from iwf.workflow_state import T, WorkflowState


@dataclass
class Form:
    username: str
    email: str
    firstname: str
    lastname: str


data_attribute_form = "form"
verify_channel = "verify"
data_attribute_status = "status"
data_attribute_verified_source = "source"


class SubmitState(WorkflowState[Form]):
    def execute(
        self,
        ctx: WorkflowContext,
        input: Form,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        persistence.set_data_attribute(data_attribute_form, input)
        persistence.set_data_attribute(data_attribute_status, "waiting")
        print(f"API to send verification email to {input.email}")
        return StateDecision.single_next_state(VerifyState)


class VerifyState(WorkflowState[None]):
    def wait_until(
        self,
        ctx: WorkflowContext,
        input: T,
        persistence: Persistence,
        communication: Communication,
    ) -> CommandRequest:
        return CommandRequest.for_any_command_completed(
            TimerCommand.by_seconds(10),  # use 10 seconds for demo
            InternalChannelCommand.by_name(verify_channel),
        )

    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        form = persistence.get_data_attribute(data_attribute_form)
        if (
            command_results.internal_channel_commands[0].status
            == ChannelRequestStatus.RECEIVED
        ):
            print(f"API to send welcome email to {form.email}")
            return StateDecision.graceful_complete_workflow("done")
        else:
            print(f"API to send the a reminder email to {form.email}")
            return StateDecision.single_next_state(VerifyState)


class UserSignupWorkflow(ObjectWorkflow):
    def get_workflow_states(self) -> StateSchema:
        return StateSchema.with_starting_state(SubmitState(), VerifyState())

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.create(
            PersistenceField.data_attribute_def(data_attribute_form, Form),
            PersistenceField.data_attribute_def(data_attribute_status, str),
            PersistenceField.data_attribute_def(data_attribute_verified_source, str),
        )

    def get_communication_schema(self) -> CommunicationSchema:
        return CommunicationSchema.create(
            CommunicationMethod.internal_channel_def(verify_channel, type(None))
        )

    @rpc(
        data_attribute_loading_policy=PersistenceLoadingPolicy(
            persistence_loading_type=PersistenceLoadingType.LOAD_PARTIAL_WITH_EXCLUSIVE_LOCK,
            locking_keys=[data_attribute_form, data_attribute_status, data_attribute_verified_source],
        )
    )
    def verify(
        self, source: str, persistence: Persistence, communication: Communication
    ) -> str:
        status = persistence.get_data_attribute(data_attribute_status)
        if status == "verified":
            return "already verified"
        persistence.set_data_attribute(data_attribute_status, "verified")
        persistence.set_data_attribute(data_attribute_verified_source, source)
        communication.publish_to_internal_channel(verify_channel)
        return "done"

    @rpc(
        data_attribute_loading_policy=PersistenceLoadingPolicy(
            persistence_loading_type=PersistenceLoadingType.LOAD_PARTIAL_WITH_EXCLUSIVE_LOCK,
            locking_keys=[data_attribute_form, data_attribute_status, data_attribute_verified_source],
        )
    )
    def describe(self, persistence: Persistence) -> tuple:
        form = persistence.get_data_attribute(data_attribute_form)
        status = persistence.get_data_attribute(data_attribute_status)
        source = persistence.get_data_attribute(data_attribute_verified_source)
        return form, status, source
