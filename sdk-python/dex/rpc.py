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
from functools import wraps
from inspect import signature
from typing import Callable, Optional

from dex.errors import WorkflowDefinitionError
from dex.dex_api.models import PersistenceLoadingPolicy, PersistenceLoadingType


@dataclass
class RPCInfo:
    method_func: Callable
    timeout_seconds: int
    input_type: Optional[type] = None
    data_attribute_loading_policy: Optional[PersistenceLoadingPolicy] = None
    params_order: Optional[list] = (
        None  # store this so that the rpc can be invoked with correct parameters
    )
    bypass_caching_for_strong_consistency: bool = False


rpc_definition_err = WorkflowDefinitionError(
    "an RPC must have at most 5 params: self, context:WorkflowContext, input:Any, persistence:Persistence, "
    "communication:Communication, where input can be any type"
)


def rpc(
    timeout_seconds: int = 10,
    data_attribute_loading_policy: Optional[PersistenceLoadingPolicy] = None,
    bypass_caching_for_strong_consistency: bool = False,
):
    def decorator(func):
        # preserve the properties of the original function.
        @wraps(func)
        def wrapper(*args, **kwargs):
            # TODO need to add type hint for decorated method
            return func(*args, **kwargs)

        wrapper._is_dex_rpc = True
        rpc_info = RPCInfo(
            method_func=func,
            timeout_seconds=timeout_seconds,
            data_attribute_loading_policy=data_attribute_loading_policy,
            bypass_caching_for_strong_consistency=bypass_caching_for_strong_consistency,
        )
        params = signature(func).parameters

        from inspect import _empty  # ignored.
        from dex.persistence import Persistence
        from dex.workflow_context import WorkflowContext
        from dex.communication import Communication

        valid_param_types_exclude_input = {
            _empty: True,
            Persistence: True,
            WorkflowContext: True,
            Communication: True,
        }
        need_persistence = False
        params_order = []
        if len(params) > 5:
            raise rpc_definition_err

        has_input = False
        for k, v in params.items():
            if k != "self":
                params_order.append(v.annotation)

            if v.annotation == Persistence:
                need_persistence = True
            if v.annotation not in valid_param_types_exclude_input:
                if not has_input:
                    has_input = True
                    rpc_info.input_type = v.annotation
                else:
                    raise rpc_definition_err
        if not need_persistence:
            rpc_info.data_attribute_loading_policy = PersistenceLoadingPolicy(
                persistence_loading_type=PersistenceLoadingType.LOAD_NONE
            )
        rpc_info.params_order = params_order
        wrapper._rpc_info = rpc_info
        return wrapper

    return decorator
