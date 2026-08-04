# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

import http
from dataclasses import dataclass
from http import HTTPStatus
from httpx import Timeout
from typing import Any, List, Optional, Type, TypeVar

from dex.client_options import ClientOptions
from dex.errors import (
    WorkflowDefinitionError,
    WorkflowRPCAcquiringLockFailure,
    WorkflowRPCExecutionError,
    WorkflowStillRunningError,
    parse_unexpected_error,
    process_http_error,
    process_workflow_abnormal_exit_error,
    InvalidArgumentError,
)
from dex.dex_api import Client, errors
from dex.dex_api.api.default import (
    post_api_v1_workflow_dataobjects_get,
    post_api_v1_workflow_dataobjects_set,
    post_api_v1_workflow_get,
    post_api_v1_workflow_reset,
    post_api_v1_workflow_rpc,
    post_api_v1_workflow_search,
    post_api_v1_workflow_searchattributes_get,
    post_api_v1_workflow_searchattributes_set,
    post_api_v1_workflow_signal,
    post_api_v1_workflow_start,
    post_api_v1_workflow_stop,
    post_api_v1_workflow_timer_skip,
    post_api_v_1_workflow_get_with_wait,
    post_api_v_1_workflow_wait_for_state_completion,
)
from dex.dex_api.models import (
    EncodedObject,
    ErrorResponse,
    IDReusePolicy,
    PersistenceLoadingPolicy,
    SearchAttribute,
    SearchAttributeKeyAndType,
    WorkflowConfig,
    WorkflowGetDataObjectsRequest,
    WorkflowGetRequest,
    WorkflowGetResponse,
    WorkflowGetSearchAttributesRequest,
    WorkflowGetSearchAttributesResponse,
    WorkflowResetRequest,
    WorkflowRetryPolicy,
    WorkflowRpcRequest,
    WorkflowRpcResponse,
    WorkflowSearchRequest,
    WorkflowSearchResponse,
    WorkflowSignalRequest,
    WorkflowSkipTimerRequest,
    WorkflowStartOptions,
    WorkflowStartRequest,
    WorkflowStateOptions,
    WorkflowStatus,
    WorkflowStopRequest,
    WorkflowAlreadyStartedOptions,
    KeyValue,
    WorkflowSetSearchAttributesRequest,
    WorkflowWaitForStateCompletionRequest,
)
from dex.dex_api.models.workflow_get_data_objects_response import (
    WorkflowGetDataObjectsResponse,
)
from dex.dex_api.models.workflow_set_data_objects_request import (
    WorkflowSetDataObjectsRequest,
)
from dex.dex_api.models.workflow_start_response import WorkflowStartResponse
from dex.dex_api.types import Response
from dex.reset_workflow_type_and_options import ResetWorkflowTypeAndOptions
from dex.stop_workflow_options import StopWorkflowOptions
from dex.utils.dex_typing import assert_not_unset
from dex.utils.persistence_utils import get_search_attribute_value

@dataclass
class UnregisteredWorkflowOptions:
    workflow_id_reuse_policy: Optional[IDReusePolicy] = None
    cron_schedule: Optional[str] = None
    workflow_start_delay_seconds: Optional[int] = None
    workflow_retry_policy: Optional[WorkflowRetryPolicy] = None
    start_state_options: Optional[WorkflowStateOptions] = None
    workflow_config_override: Optional[WorkflowConfig] = None
    workflow_already_started_options: Optional[WorkflowAlreadyStartedOptions] = None
    initial_data_attributes: Optional[dict[str, Any]] = None
    wait_for_completion_state_execution_ids: Optional[list[str]] = None
    wait_for_completion_state_ids: Optional[list[str]] = None
    initial_search_attributes: Optional[list[SearchAttribute]] = None
    using_memo_for_data_attributes: Optional[bool] = None

T = TypeVar("T")

# from https://stackoverflow.com/questions/45028991/best-way-to-extend-httpstatus-with-custom-value
# HERE BE DRAGONS!
# DO NOT do this unless you absolutely have to.
def add_http_status(name, value, phrase, description=""):
    # call our new member factory, it's essentially the `HTTPStatus.__new__` method
    new_status = HTTPStatus.__new_member__(HTTPStatus, value, phrase, description)
    new_status._name_ = name  # store the enum's member internal name
    new_status.__objclass__ = (
        HTTPStatus.__class__
    )  # store the enum's member parent class
    setattr(HTTPStatus, name, new_status)  # add it to the global HTTPStatus namespace
    HTTPStatus._member_map_[name] = new_status  # add it to the name=>member map
    HTTPStatus._member_names_.append(
        name
    )  # append the names so it appears in __members__
    HTTPStatus._value2member_map_[value] = new_status  # add it to the value=>member map

add_http_status("DEX_CUSTOM_ERROR_1", 420, "DEX_CUSTOM_ERROR_1")
add_http_status("DEX_CUSTOM_ERROR_2", 450, "DEX_CUSTOM_ERROR_2")

class UnregisteredClient:
    def __init__(self, client_options: ClientOptions):
        self.client_options = client_options
        self.api_client = Client(
            base_url=client_options.server_url,
            timeout=Timeout(client_options.api_timeout),
            raise_on_unexpected_status=True,
        )

    def start_workflow(
        self,
        workflow_type: str,
        workflow_id: str,
        start_state_id: Optional[str],
        workflow_timeout_seconds: int,
        input: Optional[Any],
        options: Optional[UnregisteredWorkflowOptions],
    ) -> str:
        request = WorkflowStartRequest(
            workflow_id=workflow_id,
            dex_worker_url=self.client_options.worker_url,
            dex_workflow_type=workflow_type,
            workflow_timeout_seconds=workflow_timeout_seconds,
            state_input=self.client_options.object_encoder.encode(input),
        )
        if start_state_id:
            request.start_state_id = start_state_id

        if options:
            if options.start_state_options:
                request.state_options = options.start_state_options

            start_options = WorkflowStartOptions()
            if options.workflow_id_reuse_policy:
                start_options.id_reuse_policy = options.workflow_id_reuse_policy
            if options.cron_schedule:
                start_options.cron_schedule = options.cron_schedule
            if options.workflow_start_delay_seconds:
                start_options.workflow_start_delay_seconds = (
                    options.workflow_start_delay_seconds
                )
            if options.workflow_retry_policy:
                start_options.retry_policy = options.workflow_retry_policy

            if options.workflow_config_override:
                start_options.workflow_config_override = (
                    options.workflow_config_override
                )

            if options.workflow_already_started_options:
                start_options.workflow_already_started_options = (
                    options.workflow_already_started_options
                )

            if options.initial_search_attributes:
                for search_attribute in options.initial_search_attributes:
                    val = get_search_attribute_value(
                        assert_not_unset(search_attribute.value_type), search_attribute
                    )
                    if val is None:
                        raise InvalidArgumentError(
                            f"search attribute value is not set correctly for key {search_attribute.key} with value type {search_attribute.value_type}"
                        )
                start_options.search_attributes = options.initial_search_attributes

            if options.initial_data_attributes:
                das = []
                for key, value in options.initial_data_attributes.items():
                    das.append(
                        KeyValue(key, self.client_options.object_encoder.encode(value))
                    )
                start_options.data_attributes = das

            if options.using_memo_for_data_attributes:
                start_options.use_memo_for_data_attributes = (
                    options.using_memo_for_data_attributes
                )

            request.workflow_start_options = start_options

            if options.wait_for_completion_state_execution_ids:
                request.wait_for_completion_state_execution_ids = (
                    options.wait_for_completion_state_execution_ids
                )

            if options.wait_for_completion_state_ids:
                request.wait_for_completion_state_ids = (
                    options.wait_for_completion_state_ids
                )

        workflow_start_response = handler_error_and_return(
            post_api_v1_workflow_start.sync_detailed(
                client=self.api_client,
                body=request,
            )
        )
        if workflow_start_response is None or not isinstance(
            workflow_start_response, WorkflowStartResponse
        ):
            raise RuntimeError("Failed to parse WorkflowStartResponse")

        return workflow_start_response.workflow_run_id

    def get_simple_workflow_result_with_wait(
        self,
        workflow_id: str,
        workflow_run_id: str,
        type_hint: Optional[Type[T]] = None,
    ) -> Optional[T]:
        """For most cases, a workflow only has one result(one completion state)
        Use this API to retrieve the output of the state"""
        request = WorkflowGetRequest(
            workflow_id=workflow_id,
            workflow_run_id=workflow_run_id,
            needs_results=True,
        )

        try:
            response = post_api_v_1_workflow_get_with_wait.sync_detailed(
                client=self.api_client,
                body=request,
            )
        except errors.UnexpectedStatus as err:
            err_resp = parse_unexpected_error(err)
            if err.status_code == 420:
                raise WorkflowStillRunningError(err.status_code, err_resp)
            else:
                raise RuntimeError(f"unknown error code {err.status_code}")

        if response.status_code != http.HTTPStatus.OK:
            assert isinstance(response.parsed, ErrorResponse)
            raise process_http_error(response.status_code, response.parsed)

        parsed = response.parsed
        assert isinstance(parsed, WorkflowGetResponse)
        if parsed.workflow_status != WorkflowStatus.COMPLETED:
            raise process_workflow_abnormal_exit_error(parsed)

        if not parsed.results or len(parsed.results) == 0:
            return None
        filtered_results = [
            result for result in parsed.results if result.completed_state_output
        ]
        if len(filtered_results) == 0:
            return None
        elif len(filtered_results) == 1:
            result = filtered_results[0]
        else:
            raise WorkflowDefinitionError(
                "workflow must have one or zero state output for using this API"
            )

        assert isinstance(result.completed_state_output, EncodedObject)
        return self.client_options.object_encoder.decode(
            result.completed_state_output,
            type_hint,
        )

    def invoke_rpc(
        self,
        input: Any,
        workflow_id: str,
        workflow_run_id: str,
        rpc_name: str,
        timeout_seconds: int,
        data_attribute_policy: Optional[PersistenceLoadingPolicy],
        all_defined_search_attribute_types: list[SearchAttributeKeyAndType],
        return_type_hint: Optional[Type[T]] = None,
        use_memo_for_data_attributes: bool = False,
        search_attribute_policy: Optional[PersistenceLoadingPolicy] = None,
    ) -> Optional[T]:
        request = WorkflowRpcRequest(
            input_=self.client_options.object_encoder.encode(input),
            workflow_id=workflow_id,
            workflow_run_id=workflow_run_id,
            rpc_name=rpc_name,
            timeout_seconds=timeout_seconds,
            use_memo_for_data_attributes=use_memo_for_data_attributes,
            search_attributes=all_defined_search_attribute_types,
        )
        if data_attribute_policy is not None:
            request.data_attributes_loading_policy = data_attribute_policy
        if search_attribute_policy is not None:
            request.search_attributes_loading_policy = search_attribute_policy

        try:
            response = post_api_v1_workflow_rpc.sync_detailed(
                client=self.api_client,
                body=request,
            )
        except errors.UnexpectedStatus as err:
            err_resp = parse_unexpected_error(err)
            if err.status_code == 420:
                raise WorkflowRPCExecutionError(err.status_code, err_resp)
            if err.status_code == 450:
                raise WorkflowRPCAcquiringLockFailure(err.status_code, err_resp)
            else:
                raise RuntimeError(f"unknown error code {err.status_code}")

        if response.status_code != http.HTTPStatus.OK:
            assert isinstance(response.parsed, ErrorResponse)
            raise process_http_error(response.status_code, response.parsed)
        assert isinstance(response.parsed, WorkflowRpcResponse)
        return self.client_options.object_encoder.decode(
            response.parsed.output,
            return_type_hint,
        )

    def signal_workflow(
        self,
        workflow_id: str,
        workflow_run_id: str,
        signal_channel_name: str,
        signal_value: Optional[Any],
    ):
        request = WorkflowSignalRequest(
            workflow_id=workflow_id,
            workflow_run_id=workflow_run_id,
            signal_channel_name=signal_channel_name,
            signal_value=self.client_options.object_encoder.encode(signal_value),
        )

        response = post_api_v1_workflow_signal.sync_detailed(
            client=self.api_client,
            body=request,
        )
        handler_error_and_return(response)

    def reset_workflow(
        self,
        workflow_id: str,
        workflow_run_id: str,
        reset_workflow_type_and_options: ResetWorkflowTypeAndOptions,
    ) -> None:
        request = WorkflowResetRequest(
            workflow_id=workflow_id,
            workflow_run_id=workflow_run_id,
            reset_type=reset_workflow_type_and_options.reset_type,
            reason=reset_workflow_type_and_options.reason,
        )
        if reset_workflow_type_and_options.history_event_id:
            request.history_event_id = reset_workflow_type_and_options.history_event_id
        if reset_workflow_type_and_options.history_event_time:
            request.history_event_time = (
                reset_workflow_type_and_options.history_event_time
            )
        if reset_workflow_type_and_options.state_id:
            request.state_id = reset_workflow_type_and_options.state_id
        if reset_workflow_type_and_options.state_execution_id:
            request.state_execution_id = (
                reset_workflow_type_and_options.state_execution_id
            )
        if reset_workflow_type_and_options.skip_signal_reapply:
            request.skip_signal_reapply = (
                reset_workflow_type_and_options.skip_signal_reapply
            )

        response = post_api_v1_workflow_reset.sync_detailed(
            client=self.api_client,
            body=request,
        )
        return handler_error_and_return(response)

    def skip_timer_by_command_id(
        self,
        workflow_id: str,
        workflow_run_id: str,
        workflow_state_id: str,
        timer_command_id: str,
        state_execution_number: int,
    ):
        request = WorkflowSkipTimerRequest(
            workflow_id=workflow_id,
            workflow_run_id=workflow_run_id,
            workflow_state_execution_id=f"{workflow_state_id}-{state_execution_number}",
            timer_command_id=timer_command_id,
        )
        response = post_api_v1_workflow_timer_skip.sync_detailed(
            client=self.api_client,
            body=request,
        )
        handler_error_and_return(response)

    def skip_timer_at_command_index(
        self,
        workflow_id: str,
        workflow_run_id: str,
        workflow_state_id: str,
        state_execution_number: int,
        timer_command_index: int,
    ):
        request = WorkflowSkipTimerRequest(
            workflow_id=workflow_id,
            workflow_run_id=workflow_run_id,
            workflow_state_execution_id=f"{workflow_state_id}-{state_execution_number}",
            timer_command_index=timer_command_index,
        )
        response = post_api_v1_workflow_timer_skip.sync_detailed(
            client=self.api_client,
            body=request,
        )
        handler_error_and_return(response)

    def stop_workflow(
        self,
        workflow_id: str,
        workflow_run_id: str,
        options: Optional[StopWorkflowOptions] = None,
    ):
        request = WorkflowStopRequest(
            workflow_id=workflow_id,
            workflow_run_id=workflow_run_id,
        )
        if options:
            request.stop_type = options.workflow_stop_type
            request.reason = options.reason
        response = post_api_v1_workflow_stop.sync_detailed(
            client=self.api_client,
            body=request,
        )
        return handler_error_and_return(response)

    def get_workflow(
        self,
        workflow_id: str,
        workflow_run_id: str,
    ) -> WorkflowGetResponse:
        request = WorkflowGetRequest(
            workflow_id=workflow_id,
            workflow_run_id=workflow_run_id,
        )
        response = post_api_v1_workflow_get.sync_detailed(
            client=self.api_client,
            body=request,
        )
        return handler_error_and_return(response)

    def get_workflow_data_attributes(
        self,
        workflow_id: str,
        workflow_run_id: str,
        attribute_keys: Optional[List[str]] = None,
        use_memo_for_data_attributes: Optional[bool] = None,
    ) -> WorkflowGetDataObjectsResponse:
        request = WorkflowGetDataObjectsRequest(
            workflow_id=workflow_id,
            workflow_run_id=workflow_run_id,
        )
        if attribute_keys:
            request.keys = attribute_keys
        if use_memo_for_data_attributes is not None:
            request.use_memo_for_data_attributes = use_memo_for_data_attributes
        response = post_api_v1_workflow_dataobjects_get.sync_detailed(
            client=self.api_client,
            body=request,
        )
        return handler_error_and_return(response)

    def set_workflow_data_attributes(
        self, workflow_id: str, workflow_run_id: str, attrs: dict[str, Any]
    ):
        objects = [
            KeyValue(key, self.client_options.object_encoder.encode(value))
            for key, value in attrs.items()
        ]

        request = WorkflowSetDataObjectsRequest(
            workflow_id,
            workflow_run_id,
            objects,
        )

        response = post_api_v1_workflow_dataobjects_set.sync_detailed(
            client=self.api_client, body=request
        )
        return handler_error_and_return(response)

    def search_workflow(
        self,
        request: WorkflowSearchRequest,
    ) -> WorkflowSearchResponse:
        response = post_api_v1_workflow_search.sync_detailed(
            client=self.api_client,
            body=request,
        )
        return handler_error_and_return(response)

    def get_workflow_search_attributes(
        self,
        workflow_id: str,
        workflow_run_id: str,
        attribute_keys: Optional[List[SearchAttributeKeyAndType]] = None,
    ) -> WorkflowGetSearchAttributesResponse:
        request = WorkflowGetSearchAttributesRequest(
            workflow_id=workflow_id,
            workflow_run_id=workflow_run_id,
        )
        if attribute_keys:
            request.keys = attribute_keys
        response = post_api_v1_workflow_searchattributes_get.sync_detailed(
            client=self.api_client,
            body=request,
        )
        return handler_error_and_return(response)

    def set_workflow_search_attributes(
        self,
        workflow_id: str,
        workflow_run_id: str,
        search_attributes: list[SearchAttribute],
    ):
        request = WorkflowSetSearchAttributesRequest(
            workflow_id=workflow_id,
            workflow_run_id=workflow_run_id,
            search_attributes=search_attributes,
        )
        response = post_api_v1_workflow_searchattributes_set.sync_detailed(
            client=self.api_client,
            body=request,
        )
        return handler_error_and_return(response)

    def wait_for_state_execution_completion_with_state_execution_id(
        self, workflow_id: str, state_execution_id: str
    ):
        request = WorkflowWaitForStateCompletionRequest(
            workflow_id=workflow_id,
            state_execution_id=state_execution_id,
            wait_time_seconds=self.client_options.long_poll_api_max_wait_time_seconds,
        )

        response = post_api_v_1_workflow_wait_for_state_completion.sync_detailed(
            client=self.api_client,
            body=request,
        )

        return handler_error_and_return(response)

    def wait_for_state_execution_completion_with_wait_for_key(
        self, workflow_id: str, state_id: str, wait_for_key: str
    ):
        request = WorkflowWaitForStateCompletionRequest(
            workflow_id=workflow_id,
            state_id=state_id,
            wait_for_key=wait_for_key,
            wait_time_seconds=self.client_options.long_poll_api_max_wait_time_seconds,
        )

        response = post_api_v_1_workflow_wait_for_state_completion.sync_detailed(
            client=self.api_client,
            body=request,
        )

        return handler_error_and_return(response)

def handler_error_and_return(response: Response):
    if response.status_code != http.HTTPStatus.OK:
        assert isinstance(response.parsed, ErrorResponse)
        raise process_http_error(response.status_code, response.parsed)
    return response.parsed
