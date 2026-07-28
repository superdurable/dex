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

from abc import ABC

from dex.communication_schema import CommunicationSchema
from dex.persistence_options import PersistenceOptions
from dex.persistence_schema import PersistenceSchema
from dex.state_schema import StateSchema


class ObjectWorkflow(ABC):
    """ObjectWorkflow is the interface to define a workflow definition.
    ObjectWorkflow is a top level concept in Dex. Any object that is long-lasting
    can be modeled as an ObjectWorkflow.
    """

    def get_workflow_states(self) -> StateSchema:
        """
        GetWorkflowStates defines the states of the workflow. A state represents
            a step of the workflow state machine.
            A state can execute some commands (signal/timer) and wait for result
            See more details in the WorkflowState interface.
            It can return an empty list, meaning no states.
            There can be at most one startingState in the list.
            If there is no startingState or with the default empty state list, the workflow
            will not start any state execution after workflow stated. Application can still
            use RPC to invoke new state execution in the future.
        Returns:
            A list of workflow state definitions. Default to empty.
        """
        return StateSchema()

    def get_persistence_schema(self) -> PersistenceSchema:
        """
        GetPersistenceSchema defines all the persistence fields for this workflow, includes:
          1. Data attributes
          2. Search attributes
        Data attributes can be read/upsert in WorkflowState WaitUntil/Execute API
        Data attributes  can also be read by getDataAttributes API by external applications using Client
        Search attributes can be read/upsert in WorkflowState WaitUntil/Execute API
        Search attributes can also be read by GetSearchAttributes Client API by external applications.
        External applications can also use "SearchWorkflow" API to find workflows by SQL-like query

        Returns:
            A persistence schema. Default to empty.
        """
        return PersistenceSchema()

    def get_persistence_options(self) -> PersistenceOptions:
        return PersistenceOptions.get_default()

    def get_communication_schema(self) -> CommunicationSchema:
        """
        GetCommunicationSchema defines all the communication methods for this workflow, this includes
          1. Signal channel
          2. Interstate channel
        Signal channel is for external applications to send signal to workflow execution.
        ObjectWorkflow execution can listen on the signal in the WorkflowState WaitUntil API and receive in
        the WorkflowState Execute API
        InterStateChannel is for synchronization communications between WorkflowStates.
        E.g. WorkflowStateA will continue after receiving a value from WorkflowStateB

        Returns:
            A communication schema. Default to empty.
        """
        return CommunicationSchema()


def get_workflow_type(wf: ObjectWorkflow) -> str:
    return wf.__class__.__name__


def get_workflow_type_by_class(wf_class: type[ObjectWorkflow]) -> str:
    return wf_class.__name__
