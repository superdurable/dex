from google.protobuf import empty_pb2 as _empty_pb2
from google.protobuf import struct_pb2 as _struct_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class IndexType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    INDEX_TYPE_UNSPECIFIED: _ClassVar[IndexType]
    INDEX_TYPE_KEYWORD: _ClassVar[IndexType]
    INDEX_TYPE_TEXT: _ClassVar[IndexType]
    INDEX_TYPE_KEYWORD_ARRAY: _ClassVar[IndexType]
    INDEX_TYPE_INT: _ClassVar[IndexType]
    INDEX_TYPE_DOUBLE: _ClassVar[IndexType]
    INDEX_TYPE_BOOL: _ClassVar[IndexType]
    INDEX_TYPE_DATETIME: _ClassVar[IndexType]

class WaitForApiFailurePolicy(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    WAIT_FOR_API_FAILURE_POLICY_UNSPECIFIED: _ClassVar[WaitForApiFailurePolicy]
    WAIT_FOR_API_FAILURE_POLICY_FAIL_FLOW_ON_FAILURE: _ClassVar[WaitForApiFailurePolicy]
    WAIT_FOR_API_FAILURE_POLICY_PROCEED_ON_FAILURE: _ClassVar[WaitForApiFailurePolicy]

class ExecuteApiFailurePolicy(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    EXECUTE_API_FAILURE_POLICY_UNSPECIFIED: _ClassVar[ExecuteApiFailurePolicy]
    EXECUTE_API_FAILURE_POLICY_FAIL_FLOW_ON_EXECUTE_API_FAILURE: _ClassVar[ExecuteApiFailurePolicy]
    EXECUTE_API_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP: _ClassVar[ExecuteApiFailurePolicy]

class IdReusePolicy(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ID_REUSE_POLICY_UNSPECIFIED: _ClassVar[IdReusePolicy]
    ID_REUSE_POLICY_ALLOW_IF_PREVIOUS_EXISTS_ABNORMALLY: _ClassVar[IdReusePolicy]
    ID_REUSE_POLICY_ALLOW_IF_NO_RUNNING: _ClassVar[IdReusePolicy]
    ID_REUSE_POLICY_DISALLOW_REUSE: _ClassVar[IdReusePolicy]
    ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING: _ClassVar[IdReusePolicy]

class ActiveStepSearchMode(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ACTIVE_STEP_SEARCH_MODE_UNSPECIFIED: _ClassVar[ActiveStepSearchMode]
    ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL: _ClassVar[ActiveStepSearchMode]
    ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR: _ClassVar[ActiveStepSearchMode]
    ACTIVE_STEP_SEARCH_MODE_DISABLED: _ClassVar[ActiveStepSearchMode]

class StepDurability(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    STEP_DURABILITY_UNSPECIFIED: _ClassVar[StepDurability]
    STEP_DURABILITY_SYNC: _ClassVar[StepDurability]
    STEP_DURABILITY_ASYNC: _ClassVar[StepDurability]

class StopType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    STOP_TYPE_UNSPECIFIED: _ClassVar[StopType]
    STOP_TYPE_CANCEL: _ClassVar[StopType]
    STOP_TYPE_TERMINATE: _ClassVar[StopType]
    STOP_TYPE_FAIL: _ClassVar[StopType]

class FlowStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    FLOW_STATUS_UNSPECIFIED: _ClassVar[FlowStatus]
    FLOW_STATUS_RUNNING: _ClassVar[FlowStatus]
    FLOW_STATUS_COMPLETED: _ClassVar[FlowStatus]
    FLOW_STATUS_FAILED: _ClassVar[FlowStatus]
    FLOW_STATUS_TIMEOUT: _ClassVar[FlowStatus]
    FLOW_STATUS_TERMINATED: _ClassVar[FlowStatus]
    FLOW_STATUS_CANCELED: _ClassVar[FlowStatus]
    FLOW_STATUS_CONTINUED_AS_NEW: _ClassVar[FlowStatus]

class FlowErrorType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    FLOW_ERROR_TYPE_UNSPECIFIED: _ClassVar[FlowErrorType]
    FLOW_ERROR_TYPE_STEP_DECISION_FAILING_FLOW: _ClassVar[FlowErrorType]
    FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW: _ClassVar[FlowErrorType]
    FLOW_ERROR_TYPE_STEP_API_FAIL: _ClassVar[FlowErrorType]
    FLOW_ERROR_TYPE_INVALID_USER_FLOW_CODE: _ClassVar[FlowErrorType]
    FLOW_ERROR_TYPE_RPC_ACQUIRE_LOCK_FAILURE: _ClassVar[FlowErrorType]
    FLOW_ERROR_TYPE_SERVER_INTERNAL: _ClassVar[FlowErrorType]

class FlowResetType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    FLOW_RESET_TYPE_UNSPECIFIED: _ClassVar[FlowResetType]
    FLOW_RESET_TYPE_HISTORY_EVENT_ID: _ClassVar[FlowResetType]
    FLOW_RESET_TYPE_BEGINNING: _ClassVar[FlowResetType]
    FLOW_RESET_TYPE_HISTORY_EVENT_TIME: _ClassVar[FlowResetType]
    FLOW_RESET_TYPE_STEP_TYPE: _ClassVar[FlowResetType]
    FLOW_RESET_TYPE_STEP_EXECUTION_ID: _ClassVar[FlowResetType]

class ErrorSubStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ERROR_SUB_STATUS_UNSPECIFIED: _ClassVar[ErrorSubStatus]
    ERROR_SUB_STATUS_UNCATEGORIZED: _ClassVar[ErrorSubStatus]
    ERROR_SUB_STATUS_FLOW_ALREADY_STARTED: _ClassVar[ErrorSubStatus]
    ERROR_SUB_STATUS_FLOW_NOT_EXISTS: _ClassVar[ErrorSubStatus]
    ERROR_SUB_STATUS_WORKER_API_ERROR: _ClassVar[ErrorSubStatus]
    ERROR_SUB_STATUS_LONG_POLL_TIME_OUT: _ClassVar[ErrorSubStatus]

class FlowConditionalCloseType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    FLOW_CONDITIONAL_CLOSE_TYPE_UNSPECIFIED: _ClassVar[FlowConditionalCloseType]
    FLOW_CONDITIONAL_CLOSE_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY: _ClassVar[FlowConditionalCloseType]
    FLOW_CONDITIONAL_CLOSE_TYPE_GRACEFUL_COMPLETE_ON_CHANNELS_EMPTY: _ClassVar[FlowConditionalCloseType]

class WaitingConditionType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    WAITING_CONDITION_TYPE_UNSPECIFIED: _ClassVar[WaitingConditionType]
    WAITING_CONDITION_TYPE_ALL_COMPLETED: _ClassVar[WaitingConditionType]
    WAITING_CONDITION_TYPE_ANY_COMPLETED: _ClassVar[WaitingConditionType]
    WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED: _ClassVar[WaitingConditionType]

class ConditionStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CONDITION_STATUS_UNSPECIFIED: _ClassVar[ConditionStatus]
    CONDITION_STATUS_WAITING: _ClassVar[ConditionStatus]
    CONDITION_STATUS_COMPLETED: _ClassVar[ConditionStatus]
INDEX_TYPE_UNSPECIFIED: IndexType
INDEX_TYPE_KEYWORD: IndexType
INDEX_TYPE_TEXT: IndexType
INDEX_TYPE_KEYWORD_ARRAY: IndexType
INDEX_TYPE_INT: IndexType
INDEX_TYPE_DOUBLE: IndexType
INDEX_TYPE_BOOL: IndexType
INDEX_TYPE_DATETIME: IndexType
WAIT_FOR_API_FAILURE_POLICY_UNSPECIFIED: WaitForApiFailurePolicy
WAIT_FOR_API_FAILURE_POLICY_FAIL_FLOW_ON_FAILURE: WaitForApiFailurePolicy
WAIT_FOR_API_FAILURE_POLICY_PROCEED_ON_FAILURE: WaitForApiFailurePolicy
EXECUTE_API_FAILURE_POLICY_UNSPECIFIED: ExecuteApiFailurePolicy
EXECUTE_API_FAILURE_POLICY_FAIL_FLOW_ON_EXECUTE_API_FAILURE: ExecuteApiFailurePolicy
EXECUTE_API_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP: ExecuteApiFailurePolicy
ID_REUSE_POLICY_UNSPECIFIED: IdReusePolicy
ID_REUSE_POLICY_ALLOW_IF_PREVIOUS_EXISTS_ABNORMALLY: IdReusePolicy
ID_REUSE_POLICY_ALLOW_IF_NO_RUNNING: IdReusePolicy
ID_REUSE_POLICY_DISALLOW_REUSE: IdReusePolicy
ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING: IdReusePolicy
ACTIVE_STEP_SEARCH_MODE_UNSPECIFIED: ActiveStepSearchMode
ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL: ActiveStepSearchMode
ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR: ActiveStepSearchMode
ACTIVE_STEP_SEARCH_MODE_DISABLED: ActiveStepSearchMode
STEP_DURABILITY_UNSPECIFIED: StepDurability
STEP_DURABILITY_SYNC: StepDurability
STEP_DURABILITY_ASYNC: StepDurability
STOP_TYPE_UNSPECIFIED: StopType
STOP_TYPE_CANCEL: StopType
STOP_TYPE_TERMINATE: StopType
STOP_TYPE_FAIL: StopType
FLOW_STATUS_UNSPECIFIED: FlowStatus
FLOW_STATUS_RUNNING: FlowStatus
FLOW_STATUS_COMPLETED: FlowStatus
FLOW_STATUS_FAILED: FlowStatus
FLOW_STATUS_TIMEOUT: FlowStatus
FLOW_STATUS_TERMINATED: FlowStatus
FLOW_STATUS_CANCELED: FlowStatus
FLOW_STATUS_CONTINUED_AS_NEW: FlowStatus
FLOW_ERROR_TYPE_UNSPECIFIED: FlowErrorType
FLOW_ERROR_TYPE_STEP_DECISION_FAILING_FLOW: FlowErrorType
FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW: FlowErrorType
FLOW_ERROR_TYPE_STEP_API_FAIL: FlowErrorType
FLOW_ERROR_TYPE_INVALID_USER_FLOW_CODE: FlowErrorType
FLOW_ERROR_TYPE_RPC_ACQUIRE_LOCK_FAILURE: FlowErrorType
FLOW_ERROR_TYPE_SERVER_INTERNAL: FlowErrorType
FLOW_RESET_TYPE_UNSPECIFIED: FlowResetType
FLOW_RESET_TYPE_HISTORY_EVENT_ID: FlowResetType
FLOW_RESET_TYPE_BEGINNING: FlowResetType
FLOW_RESET_TYPE_HISTORY_EVENT_TIME: FlowResetType
FLOW_RESET_TYPE_STEP_TYPE: FlowResetType
FLOW_RESET_TYPE_STEP_EXECUTION_ID: FlowResetType
ERROR_SUB_STATUS_UNSPECIFIED: ErrorSubStatus
ERROR_SUB_STATUS_UNCATEGORIZED: ErrorSubStatus
ERROR_SUB_STATUS_FLOW_ALREADY_STARTED: ErrorSubStatus
ERROR_SUB_STATUS_FLOW_NOT_EXISTS: ErrorSubStatus
ERROR_SUB_STATUS_WORKER_API_ERROR: ErrorSubStatus
ERROR_SUB_STATUS_LONG_POLL_TIME_OUT: ErrorSubStatus
FLOW_CONDITIONAL_CLOSE_TYPE_UNSPECIFIED: FlowConditionalCloseType
FLOW_CONDITIONAL_CLOSE_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY: FlowConditionalCloseType
FLOW_CONDITIONAL_CLOSE_TYPE_GRACEFUL_COMPLETE_ON_CHANNELS_EMPTY: FlowConditionalCloseType
WAITING_CONDITION_TYPE_UNSPECIFIED: WaitingConditionType
WAITING_CONDITION_TYPE_ALL_COMPLETED: WaitingConditionType
WAITING_CONDITION_TYPE_ANY_COMPLETED: WaitingConditionType
WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED: WaitingConditionType
CONDITION_STATUS_UNSPECIFIED: ConditionStatus
CONDITION_STATUS_WAITING: ConditionStatus
CONDITION_STATUS_COMPLETED: ConditionStatus

class Value(_message.Message):
    __slots__ = ("internal_blob_id_for_string_value", "internal_blob_id_for_obj_value", "string_value", "obj_value", "int_value", "double_value", "bool_value", "null_value")
    INTERNAL_BLOB_ID_FOR_STRING_VALUE_FIELD_NUMBER: _ClassVar[int]
    INTERNAL_BLOB_ID_FOR_OBJ_VALUE_FIELD_NUMBER: _ClassVar[int]
    STRING_VALUE_FIELD_NUMBER: _ClassVar[int]
    OBJ_VALUE_FIELD_NUMBER: _ClassVar[int]
    INT_VALUE_FIELD_NUMBER: _ClassVar[int]
    DOUBLE_VALUE_FIELD_NUMBER: _ClassVar[int]
    BOOL_VALUE_FIELD_NUMBER: _ClassVar[int]
    NULL_VALUE_FIELD_NUMBER: _ClassVar[int]
    internal_blob_id_for_string_value: str
    internal_blob_id_for_obj_value: str
    string_value: str
    obj_value: EncodedObject
    int_value: int
    double_value: float
    bool_value: bool
    null_value: _struct_pb2.NullValue
    def __init__(self, internal_blob_id_for_string_value: _Optional[str] = ..., internal_blob_id_for_obj_value: _Optional[str] = ..., string_value: _Optional[str] = ..., obj_value: _Optional[_Union[EncodedObject, _Mapping]] = ..., int_value: _Optional[int] = ..., double_value: _Optional[float] = ..., bool_value: _Optional[bool] = ..., null_value: _Optional[_Union[_struct_pb2.NullValue, str]] = ...) -> None: ...

class EncodedObject(_message.Message):
    __slots__ = ("encoding", "payload")
    ENCODING_FIELD_NUMBER: _ClassVar[int]
    PAYLOAD_FIELD_NUMBER: _ClassVar[int]
    encoding: str
    payload: bytes
    def __init__(self, encoding: _Optional[str] = ..., payload: _Optional[bytes] = ...) -> None: ...

class AttributeWrite(_message.Message):
    __slots__ = ("key", "value", "index_config")
    KEY_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    INDEX_CONFIG_FIELD_NUMBER: _ClassVar[int]
    key: str
    value: Value
    index_config: IndexConfig
    def __init__(self, key: _Optional[str] = ..., value: _Optional[_Union[Value, _Mapping]] = ..., index_config: _Optional[_Union[IndexConfig, _Mapping]] = ...) -> None: ...

class KV(_message.Message):
    __slots__ = ("key", "value")
    KEY_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    key: str
    value: Value
    def __init__(self, key: _Optional[str] = ..., value: _Optional[_Union[Value, _Mapping]] = ...) -> None: ...

class IndexConfig(_message.Message):
    __slots__ = ("enable", "type", "index_key")
    ENABLE_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    INDEX_KEY_FIELD_NUMBER: _ClassVar[int]
    enable: bool
    type: IndexType
    index_key: str
    def __init__(self, enable: _Optional[bool] = ..., type: _Optional[_Union[IndexType, str]] = ..., index_key: _Optional[str] = ...) -> None: ...

class Context(_message.Message):
    __slots__ = ("flow_id", "run_id", "flow_started_timestamp", "step_execution_id", "first_attempt_timestamp", "attempt")
    FLOW_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    FLOW_STARTED_TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    STEP_EXECUTION_ID_FIELD_NUMBER: _ClassVar[int]
    FIRST_ATTEMPT_TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    ATTEMPT_FIELD_NUMBER: _ClassVar[int]
    flow_id: str
    run_id: str
    flow_started_timestamp: int
    step_execution_id: str
    first_attempt_timestamp: int
    attempt: int
    def __init__(self, flow_id: _Optional[str] = ..., run_id: _Optional[str] = ..., flow_started_timestamp: _Optional[int] = ..., step_execution_id: _Optional[str] = ..., first_attempt_timestamp: _Optional[int] = ..., attempt: _Optional[int] = ...) -> None: ...

class RetryPolicy(_message.Message):
    __slots__ = ("initial_interval_seconds", "backoff_coefficient", "maximum_interval_seconds", "maximum_attempts", "total_duration_seconds")
    INITIAL_INTERVAL_SECONDS_FIELD_NUMBER: _ClassVar[int]
    BACKOFF_COEFFICIENT_FIELD_NUMBER: _ClassVar[int]
    MAXIMUM_INTERVAL_SECONDS_FIELD_NUMBER: _ClassVar[int]
    MAXIMUM_ATTEMPTS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_DURATION_SECONDS_FIELD_NUMBER: _ClassVar[int]
    initial_interval_seconds: int
    backoff_coefficient: float
    maximum_interval_seconds: int
    maximum_attempts: int
    total_duration_seconds: int
    def __init__(self, initial_interval_seconds: _Optional[int] = ..., backoff_coefficient: _Optional[float] = ..., maximum_interval_seconds: _Optional[int] = ..., maximum_attempts: _Optional[int] = ..., total_duration_seconds: _Optional[int] = ...) -> None: ...

class FlowRetryPolicy(_message.Message):
    __slots__ = ("initial_interval_seconds", "backoff_coefficient", "maximum_interval_seconds", "maximum_attempts")
    INITIAL_INTERVAL_SECONDS_FIELD_NUMBER: _ClassVar[int]
    BACKOFF_COEFFICIENT_FIELD_NUMBER: _ClassVar[int]
    MAXIMUM_INTERVAL_SECONDS_FIELD_NUMBER: _ClassVar[int]
    MAXIMUM_ATTEMPTS_FIELD_NUMBER: _ClassVar[int]
    initial_interval_seconds: int
    backoff_coefficient: float
    maximum_interval_seconds: int
    maximum_attempts: int
    def __init__(self, initial_interval_seconds: _Optional[int] = ..., backoff_coefficient: _Optional[float] = ..., maximum_interval_seconds: _Optional[int] = ..., maximum_attempts: _Optional[int] = ...) -> None: ...

class StepOptions(_message.Message):
    __slots__ = ("wait_for_timeout_seconds", "execute_timeout_seconds", "wait_for_retry_policy", "execute_retry_policy", "wait_for_failure_policy", "execute_failure_policy", "execute_failure_proceed_step_type", "execute_failure_proceed_step_options", "skip_wait_for", "wait_for_durability_override", "execute_durability_override")
    WAIT_FOR_TIMEOUT_SECONDS_FIELD_NUMBER: _ClassVar[int]
    EXECUTE_TIMEOUT_SECONDS_FIELD_NUMBER: _ClassVar[int]
    WAIT_FOR_RETRY_POLICY_FIELD_NUMBER: _ClassVar[int]
    EXECUTE_RETRY_POLICY_FIELD_NUMBER: _ClassVar[int]
    WAIT_FOR_FAILURE_POLICY_FIELD_NUMBER: _ClassVar[int]
    EXECUTE_FAILURE_POLICY_FIELD_NUMBER: _ClassVar[int]
    EXECUTE_FAILURE_PROCEED_STEP_TYPE_FIELD_NUMBER: _ClassVar[int]
    EXECUTE_FAILURE_PROCEED_STEP_OPTIONS_FIELD_NUMBER: _ClassVar[int]
    SKIP_WAIT_FOR_FIELD_NUMBER: _ClassVar[int]
    WAIT_FOR_DURABILITY_OVERRIDE_FIELD_NUMBER: _ClassVar[int]
    EXECUTE_DURABILITY_OVERRIDE_FIELD_NUMBER: _ClassVar[int]
    wait_for_timeout_seconds: int
    execute_timeout_seconds: int
    wait_for_retry_policy: RetryPolicy
    execute_retry_policy: RetryPolicy
    wait_for_failure_policy: WaitForApiFailurePolicy
    execute_failure_policy: ExecuteApiFailurePolicy
    execute_failure_proceed_step_type: str
    execute_failure_proceed_step_options: StepOptions
    skip_wait_for: bool
    wait_for_durability_override: StepDurability
    execute_durability_override: StepDurability
    def __init__(self, wait_for_timeout_seconds: _Optional[int] = ..., execute_timeout_seconds: _Optional[int] = ..., wait_for_retry_policy: _Optional[_Union[RetryPolicy, _Mapping]] = ..., execute_retry_policy: _Optional[_Union[RetryPolicy, _Mapping]] = ..., wait_for_failure_policy: _Optional[_Union[WaitForApiFailurePolicy, str]] = ..., execute_failure_policy: _Optional[_Union[ExecuteApiFailurePolicy, str]] = ..., execute_failure_proceed_step_type: _Optional[str] = ..., execute_failure_proceed_step_options: _Optional[_Union[StepOptions, _Mapping]] = ..., skip_wait_for: _Optional[bool] = ..., wait_for_durability_override: _Optional[_Union[StepDurability, str]] = ..., execute_durability_override: _Optional[_Union[StepDurability, str]] = ...) -> None: ...

class FlowAlreadyStartedOptions(_message.Message):
    __slots__ = ("ignore_already_started_error", "request_id")
    IGNORE_ALREADY_STARTED_ERROR_FIELD_NUMBER: _ClassVar[int]
    REQUEST_ID_FIELD_NUMBER: _ClassVar[int]
    ignore_already_started_error: bool
    request_id: str
    def __init__(self, ignore_already_started_error: _Optional[bool] = ..., request_id: _Optional[str] = ...) -> None: ...

class FlowStartOptions(_message.Message):
    __slots__ = ("id_reuse_policy", "cron_schedule", "flow_start_delay_seconds", "retry_policy", "attributes", "flow_config_override", "flow_already_started_options")
    ID_REUSE_POLICY_FIELD_NUMBER: _ClassVar[int]
    CRON_SCHEDULE_FIELD_NUMBER: _ClassVar[int]
    FLOW_START_DELAY_SECONDS_FIELD_NUMBER: _ClassVar[int]
    RETRY_POLICY_FIELD_NUMBER: _ClassVar[int]
    ATTRIBUTES_FIELD_NUMBER: _ClassVar[int]
    FLOW_CONFIG_OVERRIDE_FIELD_NUMBER: _ClassVar[int]
    FLOW_ALREADY_STARTED_OPTIONS_FIELD_NUMBER: _ClassVar[int]
    id_reuse_policy: IdReusePolicy
    cron_schedule: str
    flow_start_delay_seconds: int
    retry_policy: FlowRetryPolicy
    attributes: _containers.RepeatedCompositeFieldContainer[AttributeWrite]
    flow_config_override: FlowConfig
    flow_already_started_options: FlowAlreadyStartedOptions
    def __init__(self, id_reuse_policy: _Optional[_Union[IdReusePolicy, str]] = ..., cron_schedule: _Optional[str] = ..., flow_start_delay_seconds: _Optional[int] = ..., retry_policy: _Optional[_Union[FlowRetryPolicy, _Mapping]] = ..., attributes: _Optional[_Iterable[_Union[AttributeWrite, _Mapping]]] = ..., flow_config_override: _Optional[_Union[FlowConfig, _Mapping]] = ..., flow_already_started_options: _Optional[_Union[FlowAlreadyStartedOptions, _Mapping]] = ...) -> None: ...

class FlowConfig(_message.Message):
    __slots__ = ("active_step_search_mode", "continue_as_new_threshold", "continue_as_new_page_size_in_bytes", "step_durability")
    ACTIVE_STEP_SEARCH_MODE_FIELD_NUMBER: _ClassVar[int]
    CONTINUE_AS_NEW_THRESHOLD_FIELD_NUMBER: _ClassVar[int]
    CONTINUE_AS_NEW_PAGE_SIZE_IN_BYTES_FIELD_NUMBER: _ClassVar[int]
    STEP_DURABILITY_FIELD_NUMBER: _ClassVar[int]
    active_step_search_mode: ActiveStepSearchMode
    continue_as_new_threshold: int
    continue_as_new_page_size_in_bytes: int
    step_durability: StepDurability
    def __init__(self, active_step_search_mode: _Optional[_Union[ActiveStepSearchMode, str]] = ..., continue_as_new_threshold: _Optional[int] = ..., continue_as_new_page_size_in_bytes: _Optional[int] = ..., step_durability: _Optional[_Union[StepDurability, str]] = ...) -> None: ...

class StartFlowRequest(_message.Message):
    __slots__ = ("flow_id", "flow_type", "flow_timeout_seconds", "worker_url", "start_step_type", "wait_for_completion_step_types", "wait_for_completion_step_execution_ids", "step_input", "step_options", "flow_start_options")
    FLOW_ID_FIELD_NUMBER: _ClassVar[int]
    FLOW_TYPE_FIELD_NUMBER: _ClassVar[int]
    FLOW_TIMEOUT_SECONDS_FIELD_NUMBER: _ClassVar[int]
    WORKER_URL_FIELD_NUMBER: _ClassVar[int]
    START_STEP_TYPE_FIELD_NUMBER: _ClassVar[int]
    WAIT_FOR_COMPLETION_STEP_TYPES_FIELD_NUMBER: _ClassVar[int]
    WAIT_FOR_COMPLETION_STEP_EXECUTION_IDS_FIELD_NUMBER: _ClassVar[int]
    STEP_INPUT_FIELD_NUMBER: _ClassVar[int]
    STEP_OPTIONS_FIELD_NUMBER: _ClassVar[int]
    FLOW_START_OPTIONS_FIELD_NUMBER: _ClassVar[int]
    flow_id: str
    flow_type: str
    flow_timeout_seconds: int
    worker_url: str
    start_step_type: str
    wait_for_completion_step_types: _containers.RepeatedScalarFieldContainer[str]
    wait_for_completion_step_execution_ids: _containers.RepeatedScalarFieldContainer[str]
    step_input: Value
    step_options: StepOptions
    flow_start_options: FlowStartOptions
    def __init__(self, flow_id: _Optional[str] = ..., flow_type: _Optional[str] = ..., flow_timeout_seconds: _Optional[int] = ..., worker_url: _Optional[str] = ..., start_step_type: _Optional[str] = ..., wait_for_completion_step_types: _Optional[_Iterable[str]] = ..., wait_for_completion_step_execution_ids: _Optional[_Iterable[str]] = ..., step_input: _Optional[_Union[Value, _Mapping]] = ..., step_options: _Optional[_Union[StepOptions, _Mapping]] = ..., flow_start_options: _Optional[_Union[FlowStartOptions, _Mapping]] = ...) -> None: ...

class StartFlowResponse(_message.Message):
    __slots__ = ("run_id",)
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    run_id: str
    def __init__(self, run_id: _Optional[str] = ...) -> None: ...

class PublishToChannelRequest(_message.Message):
    __slots__ = ("flow_id", "run_id", "messages")
    FLOW_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    MESSAGES_FIELD_NUMBER: _ClassVar[int]
    flow_id: str
    run_id: str
    messages: _containers.RepeatedCompositeFieldContainer[ChannelMessage]
    def __init__(self, flow_id: _Optional[str] = ..., run_id: _Optional[str] = ..., messages: _Optional[_Iterable[_Union[ChannelMessage, _Mapping]]] = ...) -> None: ...

class ChannelMessage(_message.Message):
    __slots__ = ("channel_name", "value")
    CHANNEL_NAME_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    channel_name: str
    value: Value
    def __init__(self, channel_name: _Optional[str] = ..., value: _Optional[_Union[Value, _Mapping]] = ...) -> None: ...

class StopFlowRequest(_message.Message):
    __slots__ = ("flow_id", "run_id", "reason", "stop_type")
    FLOW_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    STOP_TYPE_FIELD_NUMBER: _ClassVar[int]
    flow_id: str
    run_id: str
    reason: str
    stop_type: StopType
    def __init__(self, flow_id: _Optional[str] = ..., run_id: _Optional[str] = ..., reason: _Optional[str] = ..., stop_type: _Optional[_Union[StopType, str]] = ...) -> None: ...

class GetAttributesRequest(_message.Message):
    __slots__ = ("flow_id", "run_id", "keys", "all_keys")
    FLOW_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    KEYS_FIELD_NUMBER: _ClassVar[int]
    ALL_KEYS_FIELD_NUMBER: _ClassVar[int]
    flow_id: str
    run_id: str
    keys: _containers.RepeatedScalarFieldContainer[str]
    all_keys: bool
    def __init__(self, flow_id: _Optional[str] = ..., run_id: _Optional[str] = ..., keys: _Optional[_Iterable[str]] = ..., all_keys: _Optional[bool] = ...) -> None: ...

class GetAttributesResponse(_message.Message):
    __slots__ = ("attributes",)
    ATTRIBUTES_FIELD_NUMBER: _ClassVar[int]
    attributes: _containers.RepeatedCompositeFieldContainer[KV]
    def __init__(self, attributes: _Optional[_Iterable[_Union[KV, _Mapping]]] = ...) -> None: ...

class SetAttributesRequest(_message.Message):
    __slots__ = ("flow_id", "run_id", "attributes")
    FLOW_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    ATTRIBUTES_FIELD_NUMBER: _ClassVar[int]
    flow_id: str
    run_id: str
    attributes: _containers.RepeatedCompositeFieldContainer[AttributeWrite]
    def __init__(self, flow_id: _Optional[str] = ..., run_id: _Optional[str] = ..., attributes: _Optional[_Iterable[_Union[AttributeWrite, _Mapping]]] = ...) -> None: ...

class LoadBlobsRequest(_message.Message):
    __slots__ = ("blob_ids",)
    BLOB_IDS_FIELD_NUMBER: _ClassVar[int]
    blob_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, blob_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class LoadBlobsResponse(_message.Message):
    __slots__ = ("values",)
    class ValuesEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: Value
        def __init__(self, key: _Optional[str] = ..., value: _Optional[_Union[Value, _Mapping]] = ...) -> None: ...
    VALUES_FIELD_NUMBER: _ClassVar[int]
    values: _containers.MessageMap[str, Value]
    def __init__(self, values: _Optional[_Mapping[str, Value]] = ...) -> None: ...

class WaitForFlowRequest(_message.Message):
    __slots__ = ("flow_id", "run_id", "needs_results", "wait_time_seconds")
    FLOW_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    NEEDS_RESULTS_FIELD_NUMBER: _ClassVar[int]
    WAIT_TIME_SECONDS_FIELD_NUMBER: _ClassVar[int]
    flow_id: str
    run_id: str
    needs_results: bool
    wait_time_seconds: int
    def __init__(self, flow_id: _Optional[str] = ..., run_id: _Optional[str] = ..., needs_results: _Optional[bool] = ..., wait_time_seconds: _Optional[int] = ...) -> None: ...

class StepCompletionOutput(_message.Message):
    __slots__ = ("completed_step_type", "completed_step_execution_id", "completed_step_output")
    COMPLETED_STEP_TYPE_FIELD_NUMBER: _ClassVar[int]
    COMPLETED_STEP_EXECUTION_ID_FIELD_NUMBER: _ClassVar[int]
    COMPLETED_STEP_OUTPUT_FIELD_NUMBER: _ClassVar[int]
    completed_step_type: str
    completed_step_execution_id: str
    completed_step_output: Value
    def __init__(self, completed_step_type: _Optional[str] = ..., completed_step_execution_id: _Optional[str] = ..., completed_step_output: _Optional[_Union[Value, _Mapping]] = ...) -> None: ...

class WaitForFlowResponse(_message.Message):
    __slots__ = ("run_id", "flow_status", "results", "error_type", "error_message")
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    FLOW_STATUS_FIELD_NUMBER: _ClassVar[int]
    RESULTS_FIELD_NUMBER: _ClassVar[int]
    ERROR_TYPE_FIELD_NUMBER: _ClassVar[int]
    ERROR_MESSAGE_FIELD_NUMBER: _ClassVar[int]
    run_id: str
    flow_status: FlowStatus
    results: _containers.RepeatedCompositeFieldContainer[StepCompletionOutput]
    error_type: FlowErrorType
    error_message: str
    def __init__(self, run_id: _Optional[str] = ..., flow_status: _Optional[_Union[FlowStatus, str]] = ..., results: _Optional[_Iterable[_Union[StepCompletionOutput, _Mapping]]] = ..., error_type: _Optional[_Union[FlowErrorType, str]] = ..., error_message: _Optional[str] = ...) -> None: ...

class SearchFlowsRequest(_message.Message):
    __slots__ = ("query", "page_size", "next_page_token")
    QUERY_FIELD_NUMBER: _ClassVar[int]
    PAGE_SIZE_FIELD_NUMBER: _ClassVar[int]
    NEXT_PAGE_TOKEN_FIELD_NUMBER: _ClassVar[int]
    query: str
    page_size: int
    next_page_token: str
    def __init__(self, query: _Optional[str] = ..., page_size: _Optional[int] = ..., next_page_token: _Optional[str] = ...) -> None: ...

class SearchFlowsResponse(_message.Message):
    __slots__ = ("flow_runs", "next_page_token")
    FLOW_RUNS_FIELD_NUMBER: _ClassVar[int]
    NEXT_PAGE_TOKEN_FIELD_NUMBER: _ClassVar[int]
    flow_runs: _containers.RepeatedCompositeFieldContainer[SearchFlowsResponseEntry]
    next_page_token: str
    def __init__(self, flow_runs: _Optional[_Iterable[_Union[SearchFlowsResponseEntry, _Mapping]]] = ..., next_page_token: _Optional[str] = ...) -> None: ...

class SearchFlowsResponseEntry(_message.Message):
    __slots__ = ("flow_id", "run_id")
    FLOW_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    flow_id: str
    run_id: str
    def __init__(self, flow_id: _Optional[str] = ..., run_id: _Optional[str] = ...) -> None: ...

class ResetFlowRequest(_message.Message):
    __slots__ = ("flow_id", "run_id", "reset_type", "history_event_id", "reason", "history_event_time", "step_type", "step_execution_id", "skip_channel_messages_reapply", "skip_locking_rpc_reapply")
    FLOW_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    RESET_TYPE_FIELD_NUMBER: _ClassVar[int]
    HISTORY_EVENT_ID_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    HISTORY_EVENT_TIME_FIELD_NUMBER: _ClassVar[int]
    STEP_TYPE_FIELD_NUMBER: _ClassVar[int]
    STEP_EXECUTION_ID_FIELD_NUMBER: _ClassVar[int]
    SKIP_CHANNEL_MESSAGES_REAPPLY_FIELD_NUMBER: _ClassVar[int]
    SKIP_LOCKING_RPC_REAPPLY_FIELD_NUMBER: _ClassVar[int]
    flow_id: str
    run_id: str
    reset_type: FlowResetType
    history_event_id: int
    reason: str
    history_event_time: str
    step_type: str
    step_execution_id: str
    skip_channel_messages_reapply: bool
    skip_locking_rpc_reapply: bool
    def __init__(self, flow_id: _Optional[str] = ..., run_id: _Optional[str] = ..., reset_type: _Optional[_Union[FlowResetType, str]] = ..., history_event_id: _Optional[int] = ..., reason: _Optional[str] = ..., history_event_time: _Optional[str] = ..., step_type: _Optional[str] = ..., step_execution_id: _Optional[str] = ..., skip_channel_messages_reapply: _Optional[bool] = ..., skip_locking_rpc_reapply: _Optional[bool] = ...) -> None: ...

class ResetFlowResponse(_message.Message):
    __slots__ = ("run_id",)
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    run_id: str
    def __init__(self, run_id: _Optional[str] = ...) -> None: ...

class InvokeRPCRequest(_message.Message):
    __slots__ = ("flow_id", "run_id", "rpc_name", "input", "timeout_seconds")
    FLOW_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    RPC_NAME_FIELD_NUMBER: _ClassVar[int]
    INPUT_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_SECONDS_FIELD_NUMBER: _ClassVar[int]
    flow_id: str
    run_id: str
    rpc_name: str
    input: Value
    timeout_seconds: int
    def __init__(self, flow_id: _Optional[str] = ..., run_id: _Optional[str] = ..., rpc_name: _Optional[str] = ..., input: _Optional[_Union[Value, _Mapping]] = ..., timeout_seconds: _Optional[int] = ...) -> None: ...

class InvokeRPCResponse(_message.Message):
    __slots__ = ("output",)
    OUTPUT_FIELD_NUMBER: _ClassVar[int]
    output: Value
    def __init__(self, output: _Optional[_Union[Value, _Mapping]] = ...) -> None: ...

class SkipTimerRequest(_message.Message):
    __slots__ = ("flow_id", "run_id", "step_execution_id", "timer_condition_id", "timer_condition_index")
    FLOW_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    STEP_EXECUTION_ID_FIELD_NUMBER: _ClassVar[int]
    TIMER_CONDITION_ID_FIELD_NUMBER: _ClassVar[int]
    TIMER_CONDITION_INDEX_FIELD_NUMBER: _ClassVar[int]
    flow_id: str
    run_id: str
    step_execution_id: str
    timer_condition_id: str
    timer_condition_index: int
    def __init__(self, flow_id: _Optional[str] = ..., run_id: _Optional[str] = ..., step_execution_id: _Optional[str] = ..., timer_condition_id: _Optional[str] = ..., timer_condition_index: _Optional[int] = ...) -> None: ...

class UpdateFlowConfigRequest(_message.Message):
    __slots__ = ("flow_id", "run_id", "flow_config")
    FLOW_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    FLOW_CONFIG_FIELD_NUMBER: _ClassVar[int]
    flow_id: str
    run_id: str
    flow_config: FlowConfig
    def __init__(self, flow_id: _Optional[str] = ..., run_id: _Optional[str] = ..., flow_config: _Optional[_Union[FlowConfig, _Mapping]] = ...) -> None: ...

class WaitForStepCompletionRequest(_message.Message):
    __slots__ = ("flow_id", "step_execution_id", "step_type", "wait_time_seconds")
    FLOW_ID_FIELD_NUMBER: _ClassVar[int]
    STEP_EXECUTION_ID_FIELD_NUMBER: _ClassVar[int]
    STEP_TYPE_FIELD_NUMBER: _ClassVar[int]
    WAIT_TIME_SECONDS_FIELD_NUMBER: _ClassVar[int]
    flow_id: str
    step_execution_id: str
    step_type: str
    wait_time_seconds: int
    def __init__(self, flow_id: _Optional[str] = ..., step_execution_id: _Optional[str] = ..., step_type: _Optional[str] = ..., wait_time_seconds: _Optional[int] = ...) -> None: ...

class WaitForStepCompletionResponse(_message.Message):
    __slots__ = ("step_completion_output",)
    STEP_COMPLETION_OUTPUT_FIELD_NUMBER: _ClassVar[int]
    step_completion_output: StepCompletionOutput
    def __init__(self, step_completion_output: _Optional[_Union[StepCompletionOutput, _Mapping]] = ...) -> None: ...

class WaitForAttributeRequest(_message.Message):
    __slots__ = ("flow_id", "run_id", "condition", "wait_time_seconds")
    FLOW_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    CONDITION_FIELD_NUMBER: _ClassVar[int]
    WAIT_TIME_SECONDS_FIELD_NUMBER: _ClassVar[int]
    flow_id: str
    run_id: str
    condition: WaitForAttributeCondition
    wait_time_seconds: int
    def __init__(self, flow_id: _Optional[str] = ..., run_id: _Optional[str] = ..., condition: _Optional[_Union[WaitForAttributeCondition, _Mapping]] = ..., wait_time_seconds: _Optional[int] = ...) -> None: ...

class WaitForAttributeCondition(_message.Message):
    __slots__ = ("equal",)
    EQUAL_FIELD_NUMBER: _ClassVar[int]
    equal: WaitForAttributeEqual
    def __init__(self, equal: _Optional[_Union[WaitForAttributeEqual, _Mapping]] = ...) -> None: ...

class WaitForAttributeEqual(_message.Message):
    __slots__ = ("key", "value")
    KEY_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    key: str
    value: Value
    def __init__(self, key: _Optional[str] = ..., value: _Optional[_Union[Value, _Mapping]] = ...) -> None: ...

class TriggerContinueAsNewRequest(_message.Message):
    __slots__ = ("flow_id", "run_id")
    FLOW_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    flow_id: str
    run_id: str
    def __init__(self, flow_id: _Optional[str] = ..., run_id: _Optional[str] = ...) -> None: ...

class HealthInfo(_message.Message):
    __slots__ = ("condition", "hostname", "duration")
    CONDITION_FIELD_NUMBER: _ClassVar[int]
    HOSTNAME_FIELD_NUMBER: _ClassVar[int]
    DURATION_FIELD_NUMBER: _ClassVar[int]
    condition: str
    hostname: str
    duration: int
    def __init__(self, condition: _Optional[str] = ..., hostname: _Optional[str] = ..., duration: _Optional[int] = ...) -> None: ...

class ErrorResponse(_message.Message):
    __slots__ = ("detail", "sub_status", "original_worker_error_detail", "original_worker_error_type", "original_worker_error_status")
    DETAIL_FIELD_NUMBER: _ClassVar[int]
    SUB_STATUS_FIELD_NUMBER: _ClassVar[int]
    ORIGINAL_WORKER_ERROR_DETAIL_FIELD_NUMBER: _ClassVar[int]
    ORIGINAL_WORKER_ERROR_TYPE_FIELD_NUMBER: _ClassVar[int]
    ORIGINAL_WORKER_ERROR_STATUS_FIELD_NUMBER: _ClassVar[int]
    detail: str
    sub_status: ErrorSubStatus
    original_worker_error_detail: str
    original_worker_error_type: str
    original_worker_error_status: int
    def __init__(self, detail: _Optional[str] = ..., sub_status: _Optional[_Union[ErrorSubStatus, str]] = ..., original_worker_error_detail: _Optional[str] = ..., original_worker_error_type: _Optional[str] = ..., original_worker_error_status: _Optional[int] = ...) -> None: ...

class WorkerErrorResponse(_message.Message):
    __slots__ = ("detail", "error_type")
    DETAIL_FIELD_NUMBER: _ClassVar[int]
    ERROR_TYPE_FIELD_NUMBER: _ClassVar[int]
    detail: str
    error_type: str
    def __init__(self, detail: _Optional[str] = ..., error_type: _Optional[str] = ...) -> None: ...

class ChannelInfo(_message.Message):
    __slots__ = ("size",)
    SIZE_FIELD_NUMBER: _ClassVar[int]
    size: int
    def __init__(self, size: _Optional[int] = ...) -> None: ...

class InvokeWaitForMethodRequest(_message.Message):
    __slots__ = ("context", "flow_type", "step_type", "step_input", "attributes")
    CONTEXT_FIELD_NUMBER: _ClassVar[int]
    FLOW_TYPE_FIELD_NUMBER: _ClassVar[int]
    STEP_TYPE_FIELD_NUMBER: _ClassVar[int]
    STEP_INPUT_FIELD_NUMBER: _ClassVar[int]
    ATTRIBUTES_FIELD_NUMBER: _ClassVar[int]
    context: Context
    flow_type: str
    step_type: str
    step_input: Value
    attributes: _containers.RepeatedCompositeFieldContainer[KV]
    def __init__(self, context: _Optional[_Union[Context, _Mapping]] = ..., flow_type: _Optional[str] = ..., step_type: _Optional[str] = ..., step_input: _Optional[_Union[Value, _Mapping]] = ..., attributes: _Optional[_Iterable[_Union[KV, _Mapping]]] = ...) -> None: ...

class InvokeWaitForMethodResponse(_message.Message):
    __slots__ = ("local_activity_input", "upsert_attributes", "waiting_condition", "upsert_step_exe_locals", "record_events", "publish_to_channel")
    LOCAL_ACTIVITY_INPUT_FIELD_NUMBER: _ClassVar[int]
    UPSERT_ATTRIBUTES_FIELD_NUMBER: _ClassVar[int]
    WAITING_CONDITION_FIELD_NUMBER: _ClassVar[int]
    UPSERT_STEP_EXE_LOCALS_FIELD_NUMBER: _ClassVar[int]
    RECORD_EVENTS_FIELD_NUMBER: _ClassVar[int]
    PUBLISH_TO_CHANNEL_FIELD_NUMBER: _ClassVar[int]
    local_activity_input: str
    upsert_attributes: _containers.RepeatedCompositeFieldContainer[AttributeWrite]
    waiting_condition: WaitingCondition
    upsert_step_exe_locals: _containers.RepeatedCompositeFieldContainer[KV]
    record_events: _containers.RepeatedCompositeFieldContainer[KV]
    publish_to_channel: _containers.RepeatedCompositeFieldContainer[ChannelMessage]
    def __init__(self, local_activity_input: _Optional[str] = ..., upsert_attributes: _Optional[_Iterable[_Union[AttributeWrite, _Mapping]]] = ..., waiting_condition: _Optional[_Union[WaitingCondition, _Mapping]] = ..., upsert_step_exe_locals: _Optional[_Iterable[_Union[KV, _Mapping]]] = ..., record_events: _Optional[_Iterable[_Union[KV, _Mapping]]] = ..., publish_to_channel: _Optional[_Iterable[_Union[ChannelMessage, _Mapping]]] = ...) -> None: ...

class InvokeExecuteMethodRequest(_message.Message):
    __slots__ = ("context", "flow_type", "step_type", "step_input", "attributes", "step_exe_locals", "condition_results")
    CONTEXT_FIELD_NUMBER: _ClassVar[int]
    FLOW_TYPE_FIELD_NUMBER: _ClassVar[int]
    STEP_TYPE_FIELD_NUMBER: _ClassVar[int]
    STEP_INPUT_FIELD_NUMBER: _ClassVar[int]
    ATTRIBUTES_FIELD_NUMBER: _ClassVar[int]
    STEP_EXE_LOCALS_FIELD_NUMBER: _ClassVar[int]
    CONDITION_RESULTS_FIELD_NUMBER: _ClassVar[int]
    context: Context
    flow_type: str
    step_type: str
    step_input: Value
    attributes: _containers.RepeatedCompositeFieldContainer[KV]
    step_exe_locals: _containers.RepeatedCompositeFieldContainer[KV]
    condition_results: ConditionResults
    def __init__(self, context: _Optional[_Union[Context, _Mapping]] = ..., flow_type: _Optional[str] = ..., step_type: _Optional[str] = ..., step_input: _Optional[_Union[Value, _Mapping]] = ..., attributes: _Optional[_Iterable[_Union[KV, _Mapping]]] = ..., step_exe_locals: _Optional[_Iterable[_Union[KV, _Mapping]]] = ..., condition_results: _Optional[_Union[ConditionResults, _Mapping]] = ...) -> None: ...

class InvokeExecuteMethodResponse(_message.Message):
    __slots__ = ("local_activity_input", "step_decision", "upsert_attributes", "record_events", "upsert_step_exe_locals", "publish_to_channel")
    LOCAL_ACTIVITY_INPUT_FIELD_NUMBER: _ClassVar[int]
    STEP_DECISION_FIELD_NUMBER: _ClassVar[int]
    UPSERT_ATTRIBUTES_FIELD_NUMBER: _ClassVar[int]
    RECORD_EVENTS_FIELD_NUMBER: _ClassVar[int]
    UPSERT_STEP_EXE_LOCALS_FIELD_NUMBER: _ClassVar[int]
    PUBLISH_TO_CHANNEL_FIELD_NUMBER: _ClassVar[int]
    local_activity_input: str
    step_decision: StepDecision
    upsert_attributes: _containers.RepeatedCompositeFieldContainer[AttributeWrite]
    record_events: _containers.RepeatedCompositeFieldContainer[KV]
    upsert_step_exe_locals: _containers.RepeatedCompositeFieldContainer[KV]
    publish_to_channel: _containers.RepeatedCompositeFieldContainer[ChannelMessage]
    def __init__(self, local_activity_input: _Optional[str] = ..., step_decision: _Optional[_Union[StepDecision, _Mapping]] = ..., upsert_attributes: _Optional[_Iterable[_Union[AttributeWrite, _Mapping]]] = ..., record_events: _Optional[_Iterable[_Union[KV, _Mapping]]] = ..., upsert_step_exe_locals: _Optional[_Iterable[_Union[KV, _Mapping]]] = ..., publish_to_channel: _Optional[_Iterable[_Union[ChannelMessage, _Mapping]]] = ...) -> None: ...

class InvokeWorkerRPCRequest(_message.Message):
    __slots__ = ("context", "flow_type", "rpc_name", "input", "attributes", "channel_infos")
    class ChannelInfosEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: ChannelInfo
        def __init__(self, key: _Optional[str] = ..., value: _Optional[_Union[ChannelInfo, _Mapping]] = ...) -> None: ...
    CONTEXT_FIELD_NUMBER: _ClassVar[int]
    FLOW_TYPE_FIELD_NUMBER: _ClassVar[int]
    RPC_NAME_FIELD_NUMBER: _ClassVar[int]
    INPUT_FIELD_NUMBER: _ClassVar[int]
    ATTRIBUTES_FIELD_NUMBER: _ClassVar[int]
    CHANNEL_INFOS_FIELD_NUMBER: _ClassVar[int]
    context: Context
    flow_type: str
    rpc_name: str
    input: Value
    attributes: _containers.RepeatedCompositeFieldContainer[KV]
    channel_infos: _containers.MessageMap[str, ChannelInfo]
    def __init__(self, context: _Optional[_Union[Context, _Mapping]] = ..., flow_type: _Optional[str] = ..., rpc_name: _Optional[str] = ..., input: _Optional[_Union[Value, _Mapping]] = ..., attributes: _Optional[_Iterable[_Union[KV, _Mapping]]] = ..., channel_infos: _Optional[_Mapping[str, ChannelInfo]] = ...) -> None: ...

class InvokeWorkerRPCResponse(_message.Message):
    __slots__ = ("output", "step_decision", "upsert_attributes", "record_events", "publish_to_channel")
    OUTPUT_FIELD_NUMBER: _ClassVar[int]
    STEP_DECISION_FIELD_NUMBER: _ClassVar[int]
    UPSERT_ATTRIBUTES_FIELD_NUMBER: _ClassVar[int]
    RECORD_EVENTS_FIELD_NUMBER: _ClassVar[int]
    PUBLISH_TO_CHANNEL_FIELD_NUMBER: _ClassVar[int]
    output: Value
    step_decision: StepDecision
    upsert_attributes: _containers.RepeatedCompositeFieldContainer[AttributeWrite]
    record_events: _containers.RepeatedCompositeFieldContainer[KV]
    publish_to_channel: _containers.RepeatedCompositeFieldContainer[ChannelMessage]
    def __init__(self, output: _Optional[_Union[Value, _Mapping]] = ..., step_decision: _Optional[_Union[StepDecision, _Mapping]] = ..., upsert_attributes: _Optional[_Iterable[_Union[AttributeWrite, _Mapping]]] = ..., record_events: _Optional[_Iterable[_Union[KV, _Mapping]]] = ..., publish_to_channel: _Optional[_Iterable[_Union[ChannelMessage, _Mapping]]] = ...) -> None: ...

class StepDecision(_message.Message):
    __slots__ = ("next_steps", "conditional_close")
    NEXT_STEPS_FIELD_NUMBER: _ClassVar[int]
    CONDITIONAL_CLOSE_FIELD_NUMBER: _ClassVar[int]
    next_steps: _containers.RepeatedCompositeFieldContainer[StepMovement]
    conditional_close: FlowConditionalClose
    def __init__(self, next_steps: _Optional[_Iterable[_Union[StepMovement, _Mapping]]] = ..., conditional_close: _Optional[_Union[FlowConditionalClose, _Mapping]] = ...) -> None: ...

class FlowConditionalClose(_message.Message):
    __slots__ = ("conditional_close_type", "channel_names", "close_input")
    CONDITIONAL_CLOSE_TYPE_FIELD_NUMBER: _ClassVar[int]
    CHANNEL_NAMES_FIELD_NUMBER: _ClassVar[int]
    CLOSE_INPUT_FIELD_NUMBER: _ClassVar[int]
    conditional_close_type: FlowConditionalCloseType
    channel_names: _containers.RepeatedScalarFieldContainer[str]
    close_input: Value
    def __init__(self, conditional_close_type: _Optional[_Union[FlowConditionalCloseType, str]] = ..., channel_names: _Optional[_Iterable[str]] = ..., close_input: _Optional[_Union[Value, _Mapping]] = ...) -> None: ...

class StepMovement(_message.Message):
    __slots__ = ("step_type", "step_input", "step_options")
    STEP_TYPE_FIELD_NUMBER: _ClassVar[int]
    STEP_INPUT_FIELD_NUMBER: _ClassVar[int]
    STEP_OPTIONS_FIELD_NUMBER: _ClassVar[int]
    step_type: str
    step_input: Value
    step_options: StepOptions
    def __init__(self, step_type: _Optional[str] = ..., step_input: _Optional[_Union[Value, _Mapping]] = ..., step_options: _Optional[_Union[StepOptions, _Mapping]] = ...) -> None: ...

class ConditionCombination(_message.Message):
    __slots__ = ("condition_ids",)
    CONDITION_IDS_FIELD_NUMBER: _ClassVar[int]
    condition_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, condition_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class WaitingCondition(_message.Message):
    __slots__ = ("waiting_condition_type", "timer_conditions", "channel_conditions", "condition_combinations")
    WAITING_CONDITION_TYPE_FIELD_NUMBER: _ClassVar[int]
    TIMER_CONDITIONS_FIELD_NUMBER: _ClassVar[int]
    CHANNEL_CONDITIONS_FIELD_NUMBER: _ClassVar[int]
    CONDITION_COMBINATIONS_FIELD_NUMBER: _ClassVar[int]
    waiting_condition_type: WaitingConditionType
    timer_conditions: _containers.RepeatedCompositeFieldContainer[TimerCondition]
    channel_conditions: _containers.RepeatedCompositeFieldContainer[ChannelCondition]
    condition_combinations: _containers.RepeatedCompositeFieldContainer[ConditionCombination]
    def __init__(self, waiting_condition_type: _Optional[_Union[WaitingConditionType, str]] = ..., timer_conditions: _Optional[_Iterable[_Union[TimerCondition, _Mapping]]] = ..., channel_conditions: _Optional[_Iterable[_Union[ChannelCondition, _Mapping]]] = ..., condition_combinations: _Optional[_Iterable[_Union[ConditionCombination, _Mapping]]] = ...) -> None: ...

class TimerCondition(_message.Message):
    __slots__ = ("condition_id", "duration_seconds")
    CONDITION_ID_FIELD_NUMBER: _ClassVar[int]
    DURATION_SECONDS_FIELD_NUMBER: _ClassVar[int]
    condition_id: str
    duration_seconds: int
    def __init__(self, condition_id: _Optional[str] = ..., duration_seconds: _Optional[int] = ...) -> None: ...

class ChannelCondition(_message.Message):
    __slots__ = ("condition_id", "channel_name", "at_least", "at_most")
    CONDITION_ID_FIELD_NUMBER: _ClassVar[int]
    CHANNEL_NAME_FIELD_NUMBER: _ClassVar[int]
    AT_LEAST_FIELD_NUMBER: _ClassVar[int]
    AT_MOST_FIELD_NUMBER: _ClassVar[int]
    condition_id: str
    channel_name: str
    at_least: int
    at_most: int
    def __init__(self, condition_id: _Optional[str] = ..., channel_name: _Optional[str] = ..., at_least: _Optional[int] = ..., at_most: _Optional[int] = ...) -> None: ...

class ConditionResults(_message.Message):
    __slots__ = ("channel_results", "timer_results", "wait_for_failed")
    CHANNEL_RESULTS_FIELD_NUMBER: _ClassVar[int]
    TIMER_RESULTS_FIELD_NUMBER: _ClassVar[int]
    WAIT_FOR_FAILED_FIELD_NUMBER: _ClassVar[int]
    channel_results: _containers.RepeatedCompositeFieldContainer[ChannelResult]
    timer_results: _containers.RepeatedCompositeFieldContainer[TimerResult]
    wait_for_failed: bool
    def __init__(self, channel_results: _Optional[_Iterable[_Union[ChannelResult, _Mapping]]] = ..., timer_results: _Optional[_Iterable[_Union[TimerResult, _Mapping]]] = ..., wait_for_failed: _Optional[bool] = ...) -> None: ...

class TimerResult(_message.Message):
    __slots__ = ("condition_id", "condition_status")
    CONDITION_ID_FIELD_NUMBER: _ClassVar[int]
    CONDITION_STATUS_FIELD_NUMBER: _ClassVar[int]
    condition_id: str
    condition_status: ConditionStatus
    def __init__(self, condition_id: _Optional[str] = ..., condition_status: _Optional[_Union[ConditionStatus, str]] = ...) -> None: ...

class ChannelResult(_message.Message):
    __slots__ = ("condition_id", "condition_status", "channel_name", "value")
    CONDITION_ID_FIELD_NUMBER: _ClassVar[int]
    CONDITION_STATUS_FIELD_NUMBER: _ClassVar[int]
    CHANNEL_NAME_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    condition_id: str
    condition_status: ConditionStatus
    channel_name: str
    value: Value
    def __init__(self, condition_id: _Optional[str] = ..., condition_status: _Optional[_Union[ConditionStatus, str]] = ..., channel_name: _Optional[str] = ..., value: _Optional[_Union[Value, _Mapping]] = ...) -> None: ...
