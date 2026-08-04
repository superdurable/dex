# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from typing import Callable, Optional

from dex.communication_schema import CommunicationMethodType
from dex.errors import InvalidArgumentError, WorkflowDefinitionError
from dex.dex_api.models import SearchAttributeValueType
from dex.persistence_options import PersistenceOptions
from dex.persistence_schema import PersistenceFieldType
from dex.rpc import RPCInfo
from dex.type_store import TypeStore, Type
from dex.workflow import ObjectWorkflow, get_workflow_type
from dex.workflow_state import WorkflowState, get_state_id

class Registry:
    _workflow_store: dict[str, ObjectWorkflow]
    _starting_state_store: dict[str, WorkflowState]
    _state_store: dict[str, dict[str, WorkflowState]]
    _internal_channel_type_store: dict[str, TypeStore]
    _signal_channel_type_store: dict[str, dict[str, Optional[type]]]
    _data_attribute_types: dict[str, TypeStore]
    _search_attribute_types: dict[str, dict[str, SearchAttributeValueType]]
    _persistence_options: dict[str, PersistenceOptions]
    _rpc_infos: dict[str, dict[str, RPCInfo]]

    def __init__(self):
        self._workflow_store = dict()
        self._starting_state_store = dict()
        self._state_store = dict()
        self._internal_channel_type_store = dict()
        self._signal_channel_type_store = dict()
        self._data_attribute_types = dict()
        self._search_attribute_types = {}
        self._persistence_options = {}
        self._rpc_infos = dict()

    def add_workflow(self, wf: ObjectWorkflow):
        self._register_workflow_type(wf)
        self._register_workflow_state(wf)
        self._register_internal_channels(wf)
        self._register_signal_channels(wf)
        self._register_data_attributes(wf)
        self._register_search_attributes(wf)
        self._register_persistence_options(wf)
        self._register_workflow_rpcs(wf)

    def add_workflows(self, *wfs: ObjectWorkflow):
        for wf in wfs:
            self.add_workflow(wf)

    def get_workflow_with_check(self, wf_type: str) -> ObjectWorkflow:
        wf = self._workflow_store.get(wf_type)
        if wf is None:
            raise InvalidArgumentError(f"workflow {wf_type} is not registered")
        return wf

    def get_workflow_starting_state(self, wf_type: str) -> Optional[WorkflowState]:
        return self._starting_state_store.get(wf_type)

    def get_workflow_state_with_check(
        self, wf_type: str, state_id: str
    ) -> WorkflowState:
        states = self._state_store.get(wf_type)
        if states is None:
            raise InvalidArgumentError(f"workflow {wf_type} is not registered")
        state = states.get(state_id)
        if state is None:
            raise InvalidArgumentError(
                f"workflow {wf_type} state {state_id} is not registered"
            )
        return state

    def get_state_store(self, wf_type: str) -> dict[str, WorkflowState]:
        return self._state_store[wf_type]

    def get_internal_channel_type_store(self, wf_type: str) -> TypeStore:
        return self._internal_channel_type_store[wf_type]

    def get_signal_channel_types(self, wf_type: str) -> dict[str, Optional[type]]:
        return self._signal_channel_type_store[wf_type]

    def get_data_attribute_types(self, wf_type: str) -> TypeStore:
        return self._data_attribute_types[wf_type]

    def get_search_attribute_types(
        self, wf_type: str
    ) -> dict[str, SearchAttributeValueType]:
        return self._search_attribute_types[wf_type]

    def get_persistence_options(self, wf_type: str) -> PersistenceOptions:
        return self._persistence_options[wf_type]

    def get_rpc_infos(self, wf_type: str) -> dict[str, RPCInfo]:
        return self._rpc_infos[wf_type]

    def _register_workflow_type(self, wf: ObjectWorkflow):
        wf_type = get_workflow_type(wf)
        if wf_type in self._workflow_store:
            raise WorkflowDefinitionError("workflow type conflict: ", wf_type)
        self._workflow_store[wf_type] = wf

    def _register_internal_channels(self, wf: ObjectWorkflow):
        wf_type = get_workflow_type(wf)

        if wf_type not in self._internal_channel_type_store:
            self._internal_channel_type_store[wf_type] = TypeStore(
                Type.INTERNAL_CHANNEL
            )

        for method in wf.get_communication_schema().communication_methods:
            if method.method_type == CommunicationMethodType.InternalChannel:
                self._internal_channel_type_store[wf_type].add_internal_channel_def(
                    method
                )

    def _register_signal_channels(self, wf: ObjectWorkflow):
        wf_type = get_workflow_type(wf)
        types: dict[str, Optional[type]] = {}
        for method in wf.get_communication_schema().communication_methods:
            if method.method_type == CommunicationMethodType.SignalChannel:
                types[method.name] = method.value_type
        self._signal_channel_type_store[wf_type] = types

    def _register_data_attributes(self, wf: ObjectWorkflow):
        wf_type = get_workflow_type(wf)
        data_attribute_types: TypeStore = TypeStore(Type.DATA_ATTRIBUTE)
        for field in wf.get_persistence_schema().persistence_fields:
            if (
                field.field_type == PersistenceFieldType.DataAttribute
                or field.field_type == PersistenceFieldType.DataAttributePrefix
            ):
                data_attribute_types.add_data_attribute_def(field)
        self._data_attribute_types[wf_type] = data_attribute_types

    def _register_search_attributes(self, wf: ObjectWorkflow):
        wf_type = get_workflow_type(wf)
        types: dict[str, SearchAttributeValueType] = {}
        for field in wf.get_persistence_schema().persistence_fields:
            if field.field_type == PersistenceFieldType.SearchAttribute:
                sa_type = field.search_attribute_type
                if sa_type is None:
                    raise WorkflowDefinitionError(
                        f"Found search attribute {field.key} with no type set"
                    )
                if field.key in types:
                    raise WorkflowDefinitionError(
                        f"Search attribute {field.key} already exists"
                    )
                types[field.key] = sa_type
        self._search_attribute_types[wf_type] = types

    def _register_persistence_options(self, wf: ObjectWorkflow):
        wf_type = get_workflow_type(wf)
        self._persistence_options[wf_type] = wf.get_persistence_options()

    def _register_workflow_state(self, wf):
        wf_type = get_workflow_type(wf)
        state_map = {}
        starting_state = None
        for state_def in wf.get_workflow_states().states:
            state_id = get_state_id(state_def.state)
            if state_id in state_map:
                raise WorkflowDefinitionError(
                    f"Workflow {wf_type} cannot have duplicate stateId {state_id}"
                )
            state_map[state_id] = state_def.state
            if state_def.can_start_workflow:
                if starting_state is not None:
                    raise WorkflowDefinitionError(
                        f"Workflow {wf_type} cannot contain more than one starting "
                        f"state"
                    )
                starting_state = state_def.state
            self._state_store[wf_type] = state_map
            self._starting_state_store[wf_type] = starting_state

    @staticmethod
    def _is_decorated_by_rpc(func: Callable):
        return getattr(func, "_is_dex_rpc", False)

    @staticmethod
    def _get_rpc_info(func: Callable):
        info = getattr(func, "_rpc_info")
        assert isinstance(info, RPCInfo)
        # NOTE: we have to override the method here so that it's associated the object
        info.method_func = func
        return info

    def _register_workflow_rpcs(self, wf):
        wf_type = get_workflow_type(wf)
        rpc_infos = {}
        for method_name in dir(wf):
            method = getattr(wf, method_name)
            if callable(method) and self._is_decorated_by_rpc(method):
                rpc_infos[method_name] = self._get_rpc_info(method)
        self._rpc_infos[wf_type] = rpc_infos
