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
from typing import Any, Optional

from dex.errors import WorkflowDefinitionError
from dex.dex_api.models import (
    ExecuteApiFailurePolicy,
    PersistenceLoadingPolicy,
    RetryPolicy,
    WaitUntilApiFailurePolicy,
    WorkflowStateOptions as IdlWorkflowStateOptions,
)
from dex.dex_api.types import Unset


@dataclass
class WorkflowStateOptions:
    state_id: Optional[str] = None
    # apply for both waitUntil and execute API
    data_attributes_loading_policy: Optional[PersistenceLoadingPolicy] = None
    search_attributes_loading_policy: Optional[PersistenceLoadingPolicy] = None
    # below are wait_until API specific options:
    wait_until_api_timeout_seconds: Optional[int] = None
    wait_until_api_retry_policy: Optional[RetryPolicy] = None
    """
       By default, workflow would fail after waitUntil API retry exhausted.
       This policy to allow proceeding to the execute API after waitUntil API exhausted all retries.
       This is useful for some advanced use cases like SAGA pattern.
       RetryPolicy is required to be set with maximumAttempts or maximumAttemptsDurationSeconds for waitUntil API.
    NOTE: execute API will use commandResults to check whether the waitUntil has succeeded or not.
       See more in <a href="https://github.com/superdurable/dex/wiki/WorkflowStateOptions">wiki</a>
    """
    proceed_to_execute_when_wait_until_retry_exhausted: Optional[
        WaitUntilApiFailurePolicy
    ] = None
    wait_until_api_data_attributes_loading_policy: Optional[
        PersistenceLoadingPolicy
    ] = None
    wait_until_api_search_attributes_loading_policy: Optional[
        PersistenceLoadingPolicy
    ] = None
    # below are execute API specific options:
    execute_api_timeout_seconds: Optional[int] = None
    execute_api_retry_policy: Optional[RetryPolicy] = None
    """
        By default, workflow would fail after execute API retry exhausted.
        Set the state to proceed to the specified state after the execute API exhausted all retries
        This is useful for some advanced use cases like SAGA pattern.
        RetryPolicy is required to be set with maximumAttempts or maximumAttemptsDurationSeconds for execute API.
        Note that the failure handling state will take the same input as the failed from state.
        TODO the type should be the type is Optional[type[WorkflowState]] but -- there is an issue with circular import...
    """
    proceed_to_state_when_execute_retry_exhausted: Optional[type] = None
    execute_api_data_attributes_loading_policy: Optional[PersistenceLoadingPolicy] = (
        None
    )
    execute_api_search_attributes_loading_policy: Optional[PersistenceLoadingPolicy] = (
        None
    )


def _to_idl_state_options(
    skip_wait_until: bool,
    options: Any,  # TODO this type was Optional[WorkflowStateOptions],
    # however, type hint is not working with recursive call...
    state_store: dict[str, Any],  # TODO this type should be dict[str, WorkflowState]
) -> IdlWorkflowStateOptions:
    res = IdlWorkflowStateOptions()
    if skip_wait_until:
        res.skip_wait_until = True

    if options is None:
        return res
    assert isinstance(options, WorkflowStateOptions)

    if options.wait_until_api_search_attributes_loading_policy is not None:
        res.wait_until_api_search_attributes_loading_policy = (
            options.wait_until_api_search_attributes_loading_policy
        )
    if options.execute_api_search_attributes_loading_policy is not None:
        res.execute_api_search_attributes_loading_policy = (
            options.execute_api_search_attributes_loading_policy
        )
    if options.search_attributes_loading_policy is not None:
        res.search_attributes_loading_policy = options.search_attributes_loading_policy
    if options.wait_until_api_data_attributes_loading_policy is not None:
        res.wait_until_api_data_attributes_loading_policy = (
            options.wait_until_api_data_attributes_loading_policy
        )
    if options.execute_api_data_attributes_loading_policy is not None:
        res.execute_api_data_attributes_loading_policy = (
            options.execute_api_data_attributes_loading_policy
        )
    if options.data_attributes_loading_policy is not None:
        res.data_attributes_loading_policy = options.data_attributes_loading_policy
    if options.proceed_to_execute_when_wait_until_retry_exhausted is not None:
        res.wait_until_api_failure_policy = (
            options.proceed_to_execute_when_wait_until_retry_exhausted
        )
        if options.wait_until_api_retry_policy is None:
            raise WorkflowDefinitionError("wait_until API retry policy must be set")
        if isinstance(
            options.wait_until_api_retry_policy.maximum_attempts, Unset
        ) and isinstance(
            options.wait_until_api_retry_policy.maximum_attempts_duration_seconds, Unset
        ):
            raise WorkflowDefinitionError(
                "wait_until API retry policy must be set with maximum_attempts or maximum_attempts_duration_seconds"
            )
    if options.wait_until_api_retry_policy is not None:
        res.wait_until_api_retry_policy = options.wait_until_api_retry_policy
    if options.wait_until_api_timeout_seconds is not None:
        res.wait_until_api_timeout_seconds = options.wait_until_api_timeout_seconds
    if options.execute_api_retry_policy is not None:
        res.execute_api_retry_policy = options.execute_api_retry_policy
    if options.execute_api_timeout_seconds is not None:
        res.execute_api_timeout_seconds = options.execute_api_timeout_seconds
    if options.proceed_to_state_when_execute_retry_exhausted is not None:
        res.execute_api_failure_policy = (
            ExecuteApiFailurePolicy.PROCEED_TO_CONFIGURED_STATE
        )
        if options.execute_api_retry_policy is None:
            raise WorkflowDefinitionError("execute API retry policy must be set")
        if isinstance(
            options.execute_api_retry_policy.maximum_attempts, Unset
        ) and isinstance(
            options.execute_api_retry_policy.maximum_attempts_duration_seconds, Unset
        ):
            raise WorkflowDefinitionError(
                "execute API retry policy must be set with maximum_attempts or maximum_attempts_duration_seconds"
            )

        from dex.workflow_state import get_state_id_by_class

        res.execute_api_failure_proceed_state_id = get_state_id_by_class(
            options.proceed_to_state_when_execute_retry_exhausted
        )
        state = state_store[res.execute_api_failure_proceed_state_id]
        proceed_state_options = state.get_state_options()

        from dex.workflow_state import should_skip_wait_until

        proceed_state_idl_options = _to_idl_state_options(
            should_skip_wait_until(state), proceed_state_options, state_store
        )
        res.execute_api_failure_proceed_state_options = proceed_state_idl_options
    return res
