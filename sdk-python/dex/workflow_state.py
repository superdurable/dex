# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from abc import ABC
from typing import Generic, TypeVar, get_args

from dex.command_request import CommandRequest
from dex.command_results import CommandResults
from dex.communication import Communication
from dex.persistence import Persistence
from dex.state_decision import StateDecision
from dex.workflow_context import WorkflowContext
from dex.workflow_state_options import WorkflowStateOptions

T = TypeVar("T")

not_implemented_error_msg = "This implementation shouldn't be invoked"

class WorkflowState(ABC, Generic[T]):
    """WorkflowState is the interface to define a workflow state."""

    def wait_until(
        self,
        ctx: WorkflowContext,
        input: T,
        persistence: Persistence,
        communication: Communication,
    ) -> CommandRequest:
        """
        WaitUntil is the method to set up commands set up to wait for, before `execute` API is invoked.
        It's optional -- execute will be invoked instead if this is not implemented.

        Args:
            ctx: the context info of this API invocation, like workflow start time, workflowId, etc
            input: input: the state input
            persistence:  the API for
                    1) data attributes: defined by ObjectWorkflow interface
                    2) search attributes: defined by ObjectWorkflow interface
                    3) stateExecutionLocals: for passing data within the state execution
                    4) recordEvent: for storing some tracking info(e.g. RPC call input/output) when executing the API.
                    Note that any write API will be recorded to server after the whole waitUntil API response is accepted
            communication: the API right now only for publishing value to internalChannel
                        Note that any write API will be recorded to server after the whole waitUntil API response is accepted.

        Returns: the requested command
        """
        raise NotImplementedError(not_implemented_error_msg)

    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        """
        Execute is the method to execute and decide what to do next. Invoke after commands from WaitUntil are completed, or there is WaitUntil is not implemented for the state.

        Args:
          ctx: the context info of this API invocation, like workflow start time, workflowId, etc
          input:  the state input
          command_results: the results of the command that executed by WaitUntil
          persistence:  the API for
                1) data attributes: defined by ObjectWorkflow interface
                2) search attributes: defined by ObjectWorkflow interface
                3) stateExecutionLocals: for passing data within the state execution
                4) recordEvent: for storing some tracking info(e.g. RPC call input/output) when executing the API.
                Note that any write API will be recorded to server after the whole waitUntil API response is accepted
          communication: the API right now only for publishing value to internalChannel.
                        Note that any write API will be recorded to server after the whole execute API response is accepted.

        Returns: the decision of what to do next(e.g. transition to next states or closing workflow)
        """
        raise NotImplementedError(not_implemented_error_msg)

    def get_state_options(self) -> WorkflowStateOptions:
        """GetStateOptions can just return nil to use the default Options
        StateOptions is optional configuration to adjust the state behaviors. Default values:
             StateId:  name of the implementation class
             waitUntilApiFailurePolicy: FAIL_WORKFLOW_ON_FAILURE
             PersistenceLoadingPolicy for dataAttributes/searchAttributes: LOAD_ALL_WITHOUT_LOCKING
             waitUntil/execute API:
                timeout: 30s
                retryPolicy:
                    InitialIntervalSeconds: 1
                    MaxInternalSeconds:100
                    MaximumAttempts: 0
                    BackoffCoefficient: 2
        Returns: WorkflowStateOptions
        """
        return WorkflowStateOptions()

def get_state_id(state: WorkflowState) -> str:
    options = state.get_state_options()
    if options is None or options.state_id is None:
        return state.__class__.__name__
    return options.state_id

def get_state_id_by_class(state: type[WorkflowState]) -> str:
    return state.__name__

def should_skip_wait_until(state: WorkflowState) -> bool:
    func_name = state.wait_until.__name__
    parent_method = getattr(super(type(state), state), func_name)
    return parent_method == state.wait_until

def get_input_type(state):
    bases = state.__orig_bases__
    for b in bases:
        if b.__origin__ == WorkflowState:
            return get_args(b)[0]
    return None

def get_state_execution_id(state: type[WorkflowState], number: int):
    return f"{get_state_id_by_class(state)}-{number}"
