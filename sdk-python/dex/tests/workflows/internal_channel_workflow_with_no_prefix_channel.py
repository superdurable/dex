# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from dex.command_request import CommandRequest, InternalChannelCommand
from dex.command_results import CommandResults
from dex.communication import Communication
from dex.communication_schema import CommunicationMethod, CommunicationSchema
from dex.persistence import Persistence
from dex.state_decision import StateDecision
from dex.state_schema import StateSchema
from dex.workflow import ObjectWorkflow
from dex.workflow_context import WorkflowContext
from dex.workflow_state import T, WorkflowState

internal_channel_name = "internal-channel-1"

test_non_prefix_channel_name = "test-channel-"
test_non_prefix_channel_name_with_suffix = test_non_prefix_channel_name + "abc"

class InitState(WorkflowState[None]):
    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        return StateDecision.multi_next_states(
            WaitAnyWithPublishState, WaitAllThenPublishState
        )

class WaitAnyWithPublishState(WorkflowState[None]):
    def wait_until(
        self,
        ctx: WorkflowContext,
        input: T,
        persistence: Persistence,
        communication: Communication,
    ) -> CommandRequest:
        # Trying to publish to a non-existing channel; this would only work if test_channel_name_non_prefix was defined as a prefix channel
        communication.publish_to_internal_channel(
            test_non_prefix_channel_name_with_suffix, "str-value-for-prefix"
        )
        return CommandRequest.for_any_command_completed(
            InternalChannelCommand.by_name(internal_channel_name),
        )

    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        return StateDecision.graceful_complete_workflow()

class WaitAllThenPublishState(WorkflowState[None]):
    def wait_until(
        self,
        ctx: WorkflowContext,
        input: T,
        persistence: Persistence,
        communication: Communication,
    ) -> CommandRequest:
        return CommandRequest.for_all_command_completed(
            InternalChannelCommand.by_name(test_non_prefix_channel_name),
        )

    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        communication.publish_to_internal_channel(internal_channel_name, None)
        return StateDecision.dead_end

class InternalChannelWorkflowWithNoPrefixChannel(ObjectWorkflow):
    def get_workflow_states(self) -> StateSchema:
        return StateSchema.with_starting_state(
            InitState(), WaitAnyWithPublishState(), WaitAllThenPublishState()
        )

    def get_communication_schema(self) -> CommunicationSchema:
        return CommunicationSchema.create(
            CommunicationMethod.internal_channel_def(internal_channel_name, None),
            # Defining a standard channel (non-prefix) to make sure messages to the channel with a suffix added will not be accepted
            CommunicationMethod.internal_channel_def(test_non_prefix_channel_name, str),
        )
