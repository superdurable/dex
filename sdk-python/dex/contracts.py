# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

import json
import math
from abc import ABC, abstractmethod
from dataclasses import asdict, dataclass, field, fields, is_dataclass
from datetime import datetime, timedelta
from enum import Enum
from inspect import signature
from typing import (
    Any,
    Callable,
    Generic,
    Iterator,
    Mapping,
    Protocol,
    Sequence,
    TypeVar,
    cast,
    get_args,
    get_origin,
    get_type_hints,
    overload,
)

InputT = TypeVar("InputT")
OutputT = TypeVar("OutputT")
ValueT = TypeVar("ValueT")
StartT = TypeVar("StartT")
CallableT = TypeVar("CallableT", bound=Callable[..., Any])


class PhaseNotImplementedError(RuntimeError):
    """Raised when a contract reaches a later Rust Core phase."""


def _require_name(name: str) -> None:
    if not name.strip():
        raise ValueError("durable name is required")


def _validate_condition_id(condition_id: str | None) -> None:
    if condition_id is not None and not condition_id:
        raise ValueError("condition ID must not be empty")


class WireKind(Enum):
    STRING = "string"
    BOOL = "bool"
    INT64 = "int64"
    DOUBLE = "double"
    BYTES = "bytes"
    JSON = "json"


@dataclass(frozen=True)
class Value:
    kind: WireKind
    data: str | bool | int | float | bytes


class Codec(Protocol[ValueT]):
    @property
    def type_name(self) -> str: ...

    @property
    def wire_kind(self) -> WireKind: ...

    def encode(self, value: ValueT) -> Value: ...

    def decode(self, value: Value) -> ValueT: ...


@dataclass(frozen=True)
class _ScalarCodec(Generic[ValueT]):
    type_name: str
    wire_kind: WireKind
    expected_type: type[Any]
    validator: Callable[[ValueT], None] | None = None

    def encode(self, value: ValueT) -> Value:
        if type(value) is not self.expected_type:
            raise TypeError(
                f"{self.type_name} requires {self.expected_type.__name__}, "
                f"got {type(value).__name__}"
            )
        if self.validator is not None:
            self.validator(value)
        return Value(self.wire_kind, cast(str | bool | int | float | bytes, value))

    def decode(self, value: Value) -> ValueT:
        if value.kind is not self.wire_kind:
            raise TypeError(
                f"{self.type_name} cannot decode wire kind {value.kind.value}"
            )
        decoded = value.data
        if type(decoded) is not self.expected_type:
            raise TypeError(f"invalid {self.type_name} payload")
        typed = cast(ValueT, decoded)
        if self.validator is not None:
            self.validator(typed)
        return typed


def _validate_int64(value: int) -> None:
    if value < -(2**63) or value > 2**63 - 1:
        raise OverflowError(f"integer {value} exceeds int64")


def _validate_double(value: float) -> None:
    if not math.isfinite(value):
        raise ValueError("non-finite floating-point values are unsupported")


STRING: Codec[str] = _ScalarCodec("str", WireKind.STRING, str)
BOOL: Codec[bool] = _ScalarCodec("bool", WireKind.BOOL, bool)
INT64: Codec[int] = _ScalarCodec("int", WireKind.INT64, int, _validate_int64)
DOUBLE: Codec[float] = _ScalarCodec("float", WireKind.DOUBLE, float, _validate_double)
BYTES: Codec[bytes] = _ScalarCodec("bytes", WireKind.BYTES, bytes)


@dataclass(frozen=True)
class JsonCodec(Generic[ValueT]):
    type_name: str
    decoder: Callable[[Any], ValueT]
    encoder: Callable[[ValueT], Any] = field(default=lambda value: value)
    wire_kind: WireKind = field(default=WireKind.JSON, init=False)

    def encode(self, value: ValueT) -> Value:
        payload = json.dumps(
            self.encoder(value),
            allow_nan=False,
            separators=(",", ":"),
            sort_keys=True,
        )
        return Value(WireKind.JSON, payload)

    def decode(self, value: Value) -> ValueT:
        if value.kind is not WireKind.JSON or not isinstance(value.data, str):
            raise TypeError(f"{self.type_name} requires a JSON value")
        return self.decoder(json.loads(value.data))


class CodecRegistry:
    def __init__(self, codecs: Mapping[object, Codec[Any]] | None = None) -> None:
        self._codecs = dict(codecs or {})

    def resolve(self, type_hint: object) -> Codec[Any]:
        custom = self._codecs.get(type_hint)
        if custom is not None:
            return custom
        builtins: dict[object, Codec[Any]] = {
            str: STRING,
            bool: BOOL,
            int: INT64,
            float: DOUBLE,
            bytes: BYTES,
        }
        builtin = builtins.get(type_hint)
        if builtin is not None:
            return builtin
        if _supports_automatic_json(type_hint):
            return JsonCodec(
                _type_name(type_hint),
                lambda value: _decode_json_value(value, type_hint),
                _encode_json_value,
            )
        raise TypeError(
            f"no codec for {_type_name(type_hint)}; register one in CodecRegistry"
        )


def _supports_automatic_json(type_hint: object) -> bool:
    origin = get_origin(type_hint)
    return (
        (
            isinstance(type_hint, type)
            and (is_dataclass(type_hint) or issubclass(type_hint, Enum))
        )
        or type_hint is datetime
        or origin in (list, tuple, dict, Mapping, Sequence)
    )


def _type_name(type_hint: object) -> str:
    return getattr(type_hint, "__qualname__", str(type_hint))


def _encode_json_value(value: Any) -> Any:
    if is_dataclass(value) and not isinstance(value, type):
        return asdict(value)
    if isinstance(value, datetime):
        return value.isoformat()
    if isinstance(value, Enum):
        return value.value
    if isinstance(value, Mapping):
        return {key: _encode_json_value(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_encode_json_value(item) for item in value]
    return value


def _decode_json_value(value: Any, type_hint: object) -> Any:
    origin = get_origin(type_hint)
    arguments = get_args(type_hint)
    if isinstance(type_hint, type) and is_dataclass(type_hint):
        if not isinstance(value, Mapping):
            raise TypeError(f"{_type_name(type_hint)} requires a JSON object")
        hints = get_type_hints(type_hint)
        return type_hint(
            **{
                definition.name: _decode_json_value(
                    value[definition.name], hints[definition.name]
                )
                for definition in fields(type_hint)
            }
        )
    if type_hint is datetime:
        if not isinstance(value, str):
            raise TypeError("datetime requires a JSON string")
        return datetime.fromisoformat(value)
    if isinstance(type_hint, type) and issubclass(type_hint, Enum):
        return type_hint(value)
    if origin is list:
        return [_decode_json_value(item, arguments[0]) for item in value]
    if origin is tuple:
        return tuple(_decode_json_value(item, arguments[0]) for item in value)
    if origin in (dict, Mapping):
        return {
            _decode_json_value(key, arguments[0]): _decode_json_value(
                item, arguments[1]
            )
            for key, item in value.items()
        }
    if type_hint in (str, bool, int, float):
        if type(value) is not type_hint:
            raise TypeError(f"expected {_type_name(type_hint)}")
    return value


@dataclass(frozen=True)
class BlobCacheConfig:
    directory: str
    max_bytes: int
    frequency_counters: int = 10_000

    def __post_init__(self) -> None:
        if not self.directory:
            raise ValueError("blob cache directory is required")
        if self.max_bytes <= 0:
            raise ValueError("blob cache max_bytes must be positive")
        if self.frequency_counters < 0:
            raise ValueError("blob cache frequency_counters must not be negative")
        if self.frequency_counters == 0:
            object.__setattr__(self, "frequency_counters", 10_000)


class BlobCache(Protocol):
    @property
    def config(self) -> BlobCacheConfig: ...

    def get(self, blob_id: str) -> bytes | None: ...

    def put(self, blob_id: str, payload: bytes) -> bool: ...

    def delete(self, blob_id: str) -> None: ...

    def delete_all(self) -> None: ...

    def close(self) -> None: ...


def open_blob_cache(config: BlobCacheConfig) -> BlobCache:
    del config
    raise PhaseNotImplementedError("BlobCache bridge belongs to a later phase")


class IndexType(Enum):
    KEYWORD = "keyword"
    FULL_TEXT = "full_text"
    KEYWORD_ARRAY = "keyword_array"
    INT = "int"
    DOUBLE = "double"
    BOOL = "bool"
    DATETIME = "datetime"


@dataclass(frozen=True)
class AttributeIndex:
    type: IndexType
    index_key: str = ""


class Context(Protocol):
    @property
    def flow_id(self) -> str: ...

    @property
    def run_id(self) -> str: ...

    @property
    def step_execution_id(self) -> str: ...

    @property
    def from_step_execution_id(self) -> str: ...

    @property
    def attempt(self) -> int: ...

    def has_timer_fired(self, index: int | None = None) -> bool: ...

    def wait_for_method_failed(self) -> bool: ...

    def set_step_execution_local(self, key: str, value: object) -> None: ...

    def get_step_execution_local(
        self, key: str, value_type: type[ValueT]
    ) -> ValueT | None: ...

    def record_event(self, name: str, value: object) -> None: ...

    def _get_attribute(
        self,
        definition: Attribute[ValueT] | AttributeMap[ValueT],
        instance: str | None,
    ) -> ValueT: ...

    def _set_attribute(
        self,
        definition: Attribute[ValueT] | AttributeMap[ValueT],
        instance: str | None,
        value: ValueT,
    ) -> None: ...

    def _delete_attribute(
        self,
        definition: Attribute[object] | AttributeMap[object],
        instance: str | None,
    ) -> None: ...

    def _publish_channel(
        self,
        definition: Channel[ValueT] | ChannelMap[ValueT],
        instance: str | None,
        value: ValueT,
    ) -> None: ...

    def _channel_size(
        self,
        definition: Channel[object] | ChannelMap[object],
        instance: str | None,
    ) -> int: ...

    def _channel_results(
        self,
        definition: Channel[ValueT] | ChannelMap[ValueT],
        instance: str | None,
    ) -> Sequence[ValueT]: ...


@dataclass(frozen=True)
class Attribute(Generic[ValueT]):
    name: str
    value_type: type[ValueT]
    index: AttributeIndex | None = None

    def __post_init__(self) -> None:
        _require_name(self.name)

    def get(self, context: Context) -> ValueT:
        return context._get_attribute(self, None)

    def set(self, context: Context, value: ValueT) -> None:
        context._set_attribute(self, None, value)

    def delete(self, context: Context) -> None:
        context._delete_attribute(cast(Attribute[object], self), None)

    def lock(self) -> AttributeLock:
        return AttributeLock(self)


@dataclass(frozen=True)
class AttributeMap(Generic[ValueT]):
    name: str
    value_type: type[ValueT]
    index: AttributeIndex | None = None

    def __post_init__(self) -> None:
        _require_name(self.name)

    def get(self, context: Context, instance: str) -> ValueT:
        return context._get_attribute(self, instance)

    def set(self, context: Context, instance: str, value: ValueT) -> None:
        context._set_attribute(self, instance, value)

    def delete(self, context: Context, instance: str) -> None:
        context._delete_attribute(cast(AttributeMap[object], self), instance)

    def lock(self, instance: str) -> AttributeLock:
        _require_name(instance)
        return AttributeLock(self, instance)


@dataclass(frozen=True)
class AttributeLock:
    attribute: Attribute[Any] | AttributeMap[Any]
    instance: str | None = None


@dataclass(frozen=True)
class Condition:
    condition_id: str | None = None

    def __post_init__(self) -> None:
        _validate_condition_id(self.condition_id)


@dataclass(frozen=True)
class TimerCondition(Condition):
    duration: timedelta = timedelta(0)

    def __post_init__(self) -> None:
        super().__post_init__()
        if self.duration < timedelta(0):
            raise ValueError("timer duration must not be negative")


@dataclass(frozen=True)
class ChannelCondition(Condition, Generic[ValueT]):
    channel: Channel[ValueT] | ChannelMap[ValueT] | None = None
    instance: str | None = None
    at_least: int | None = None
    at_most: int | None = None

    def __post_init__(self) -> None:
        super().__post_init__()
        if self.channel is None:
            raise ValueError("channel condition requires a channel")
        if self.at_least is None and self.at_most is None:
            raise ValueError("channel condition requires a bound")
        if self.at_least is not None and self.at_least < 0:
            raise ValueError("at_least must not be negative")
        if self.at_most is not None and self.at_most < 0:
            raise ValueError("at_most must not be negative")
        if (
            self.at_least is not None
            and self.at_most is not None
            and self.at_most < self.at_least
        ):
            raise ValueError("at_most must not be below at_least")


@dataclass(frozen=True)
class ConditionCombination:
    conditions: tuple[Condition, ...]

    @staticmethod
    def of(*conditions: Condition) -> ConditionCombination:
        return ConditionCombination(conditions)


class Timer:
    @staticmethod
    def by_duration(
        duration: timedelta,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        return TimerCondition(condition_id=condition_id, duration=duration)


@dataclass(frozen=True)
class Channel(Generic[ValueT]):
    name: str
    value_type: type[ValueT]

    def __post_init__(self) -> None:
        _require_name(self.name)

    def publish(self, context: Context, value: ValueT) -> None:
        context._publish_channel(self, None, value)

    def size(self, context: Context) -> int:
        return context._channel_size(cast(Channel[object], self), None)

    def results(self, context: Context) -> Sequence[ValueT]:
        return context._channel_results(self, None)

    def for_one(self, *, condition_id: str | None = None) -> Condition:
        return self.for_range(at_least=1, at_most=1, condition_id=condition_id)

    def for_n(self, count: int, *, condition_id: str | None = None) -> Condition:
        return self.for_range(
            at_least=count,
            at_most=count,
            condition_id=condition_id,
        )

    def at_least(
        self,
        count: int,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        return self.for_range(at_least=count, condition_id=condition_id)

    def at_most(
        self,
        count: int,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        return self.for_range(at_most=count, condition_id=condition_id)

    def for_range(
        self,
        *,
        at_least: int | None = None,
        at_most: int | None = None,
        condition_id: str | None = None,
    ) -> Condition:
        return ChannelCondition(
            condition_id=condition_id,
            channel=self,
            at_least=at_least,
            at_most=at_most,
        )


@dataclass(frozen=True)
class ChannelMap(Generic[ValueT]):
    name: str
    value_type: type[ValueT]

    def __post_init__(self) -> None:
        _require_name(self.name)

    def publish(self, context: Context, instance: str, value: ValueT) -> None:
        context._publish_channel(self, instance, value)

    def size(self, context: Context, instance: str) -> int:
        return context._channel_size(cast(ChannelMap[object], self), instance)

    def results(self, context: Context, instance: str) -> Sequence[ValueT]:
        return context._channel_results(self, instance)

    def for_one(
        self,
        instance: str,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        return self.for_range(
            instance,
            at_least=1,
            at_most=1,
            condition_id=condition_id,
        )

    def for_n(
        self,
        instance: str,
        count: int,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        return self.for_range(
            instance,
            at_least=count,
            at_most=count,
            condition_id=condition_id,
        )

    def at_least(
        self,
        instance: str,
        count: int,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        return self.for_range(
            instance,
            at_least=count,
            condition_id=condition_id,
        )

    def at_most(
        self,
        instance: str,
        count: int,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        return self.for_range(
            instance,
            at_most=count,
            condition_id=condition_id,
        )

    def for_range(
        self,
        instance: str,
        *,
        at_least: int | None = None,
        at_most: int | None = None,
        condition_id: str | None = None,
    ) -> Condition:
        return ChannelCondition(
            condition_id=condition_id,
            channel=self,
            instance=instance,
            at_least=at_least,
            at_most=at_most,
        )


class WaitKind(Enum):
    SKIP_IMMEDIATELY = "skip_immediately"
    ALL_OF = "all_of"
    ANY_OF = "any_of"
    ANY_COMBINATION_OF = "any_combination_of"


@dataclass(frozen=True)
class Wait:
    kind: WaitKind
    conditions: tuple[Condition, ...] = ()
    combinations: tuple[ConditionCombination, ...] = ()

    @staticmethod
    def skip_immediately() -> Wait:
        return Wait(WaitKind.SKIP_IMMEDIATELY)

    @staticmethod
    def all_of(*conditions: Condition) -> Wait:
        return Wait(WaitKind.ALL_OF, conditions)

    @staticmethod
    def any_of(*conditions: Condition) -> Wait:
        return Wait(WaitKind.ANY_OF, conditions)

    @staticmethod
    def any_combination_of(*combinations: ConditionCombination) -> Wait:
        return Wait(
            WaitKind.ANY_COMBINATION_OF,
            combinations=combinations,
        )


class StepDurability(Enum):
    SYNC = "sync"
    ASYNC = "async"


class WaitForFailurePolicy(Enum):
    FAIL_FLOW = "fail_flow"
    PROCEED = "proceed"


@dataclass(frozen=True)
class RetryPolicy:
    initial_interval: timedelta | None = None
    backoff_coefficient: float = 0.0
    maximum_interval: timedelta | None = None
    maximum_attempts: int = 0
    total_duration: timedelta | None = None


@dataclass(frozen=True)
class ExecuteFailure(Generic[InputT]):
    step: Step[InputT]
    options: StepOptions | None = None

    @staticmethod
    def proceed_to(
        step: Step[InputT],
        options: StepOptions | None = None,
    ) -> ExecuteFailure[InputT]:
        return ExecuteFailure(step, options)


@dataclass(frozen=True)
class StepOptions:
    wait_for_method_timeout: timedelta | None = None
    execute_method_timeout: timedelta | None = None
    wait_for_retry: RetryPolicy | None = None
    execute_retry: RetryPolicy | None = None
    wait_for_failure: WaitForFailurePolicy | None = None
    wait_for_durability: StepDurability | None = None
    execute_durability: StepDurability | None = None
    wait_for_lock_attributes: tuple[AttributeLock, ...] = ()
    execute_lock_attributes: tuple[AttributeLock, ...] = ()
    execute_failure: ExecuteFailure[Any] | None = None


class Step(Generic[InputT], ABC):
    @abstractmethod
    def execute(self, context: Context, input: InputT) -> StepDecision:
        raise NotImplementedError

    def wait_for(self, context: Context, input: InputT) -> Wait:
        del context, input
        raise RuntimeError("framework must skip the default wait_for")

    def get_step_type(self) -> str:
        return type(self).__qualname__

    def get_step_options(self) -> StepOptions | None:
        return None


@dataclass(frozen=True)
class _StepDef:
    step: Step[Any]
    is_start_step: bool


@dataclass(frozen=True)
class StepList(Generic[StartT]):
    _definitions: tuple[_StepDef, ...]

    @classmethod
    def empty(cls) -> StepList[StartT]:
        return cls(())

    @staticmethod
    def start_step(step: Step[StartT]) -> StepList[StartT]:
        return StepList((_StepDef(step, True),))

    @classmethod
    def without_start_step(cls, *steps: Step[Any]) -> StepList[StartT]:
        return cls(tuple(_StepDef(step, False) for step in steps))

    def other_steps(self, *steps: Step[Any]) -> StepList[StartT]:
        return StepList(
            self._definitions + tuple(_StepDef(step, False) for step in steps)
        )

    def __iter__(self) -> Iterator[_StepDef]:
        return iter(self._definitions)


@dataclass(frozen=True)
class _RPCOptions:
    name: str | None
    timeout: timedelta | None
    lock_attributes: tuple[AttributeLock, ...]


@overload
def rpc(handler: CallableT) -> CallableT: ...


@overload
def rpc(
    *,
    name: str | None = None,
    timeout: timedelta | None = None,
    lock_attributes: Sequence[AttributeLock] = (),
) -> Callable[[CallableT], CallableT]: ...


def rpc(
    handler: CallableT | None = None,
    *,
    name: str | None = None,
    timeout: timedelta | None = None,
    lock_attributes: Sequence[AttributeLock] = (),
) -> CallableT | Callable[[CallableT], CallableT]:
    if name is not None:
        _require_name(name)
    if timeout is not None and timeout < timedelta(0):
        raise ValueError("RPC timeout must not be negative")

    def decorate(handler: CallableT) -> CallableT:
        setattr(
            handler,
            "__dex_rpc_options__",
            _RPCOptions(name, timeout, tuple(lock_attributes)),
        )
        return handler

    if handler is not None:
        return decorate(handler)
    return decorate


@dataclass(frozen=True)
class StepMovement(Generic[InputT]):
    step: Step[InputT]
    input: InputT
    options: StepOptions | None = None

    @staticmethod
    def of(
        step: Step[InputT],
        input: InputT,
        *,
        options: StepOptions | None = None,
    ) -> StepMovement[InputT]:
        return StepMovement(step, input, options)


class DecisionKind(Enum):
    NEXT = "next"
    GRACEFUL_COMPLETE = "graceful_complete"
    FORCE_COMPLETE = "force_complete"
    FORCE_COMPLETE_IF_CHANNELS_EMPTY = "force_complete_if_channels_empty"
    FORCE_FAIL = "force_fail"
    DEAD_END = "dead_end"


@dataclass(frozen=True)
class StepDecision:
    kind: DecisionKind
    movements: tuple[StepMovement[Any], ...] = ()
    output: object | None = None
    reason: str = ""
    empty_channels: tuple[Channel[Any] | ChannelMap[Any], ...] = ()
    fallback: StepMovement[Any] | None = None


def go_to(step: Step[InputT], input: InputT) -> StepDecision:
    return go_to_multi(StepMovement.of(step, input))


def go_to_multi(*movements: StepMovement[Any]) -> StepDecision:
    return StepDecision(DecisionKind.NEXT, movements=movements)


def graceful_complete(output: object | None = None) -> StepDecision:
    return StepDecision(DecisionKind.GRACEFUL_COMPLETE, output=output)


def force_complete(output: object | None = None) -> StepDecision:
    return StepDecision(DecisionKind.FORCE_COMPLETE, output=output)


def force_complete_when_channels_empty(
    output: object,
    fallback: StepMovement[Any],
    *channels: Channel[Any] | ChannelMap[Any],
) -> StepDecision:
    return StepDecision(
        DecisionKind.FORCE_COMPLETE_IF_CHANNELS_EMPTY,
        output=output,
        empty_channels=channels,
        fallback=fallback,
    )


def force_fail(reason: str) -> StepDecision:
    return StepDecision(DecisionKind.FORCE_FAIL, reason=reason)


def dead_end() -> StepDecision:
    return StepDecision(DecisionKind.DEAD_END)


@dataclass(frozen=True)
class RPCResult(Generic[OutputT]):
    output: OutputT
    next_steps: tuple[StepMovement[Any], ...] = ()


@dataclass(frozen=True)
class PersistenceSchema:
    attributes: tuple[Attribute[Any] | AttributeMap[Any], ...] = ()
    channels: tuple[Channel[Any] | ChannelMap[Any], ...] = ()


@dataclass(frozen=True)
class InitialAttribute(Generic[ValueT]):
    attribute: Attribute[ValueT]
    value: ValueT


class Flow(Generic[StartT], ABC):
    def get_flow_type(self) -> str:
        return type(self).__qualname__

    def get_steps(self) -> StepList[StartT]:
        return StepList.empty()

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema()


@dataclass(frozen=True)
class _RegisteredStep:
    step: Step[Any]
    input_codec: Codec[Any]


@dataclass(frozen=True)
class _RegisteredRPC:
    method: Callable[..., Any]
    input_codec: Codec[Any] | None
    output_codec: Codec[Any] | None


@dataclass(frozen=True)
class Registry:
    flows: tuple[Flow[Any], ...]
    codec_registry: CodecRegistry
    _steps: tuple[_RegisteredStep, ...]
    _rpcs: tuple[_RegisteredRPC, ...]

    def __init__(
        self,
        flows: Sequence[Flow[Any]],
        codec_registry: CodecRegistry | None = None,
    ) -> None:
        immutable_flows = tuple(flows)
        resolved_codecs = codec_registry or CodecRegistry()
        registered_steps, registered_rpcs = self._validate(
            immutable_flows, resolved_codecs
        )
        object.__setattr__(self, "flows", immutable_flows)
        object.__setattr__(self, "codec_registry", resolved_codecs)
        object.__setattr__(self, "_steps", registered_steps)
        object.__setattr__(self, "_rpcs", registered_rpcs)

    @staticmethod
    def _validate(
        flows: tuple[Flow[Any], ...], codec_registry: CodecRegistry
    ) -> tuple[tuple[_RegisteredStep, ...], tuple[_RegisteredRPC, ...]]:
        flow_names: set[str] = set()
        registered_steps: list[_RegisteredStep] = []
        registered_rpcs: list[_RegisteredRPC] = []
        for flow in flows:
            flow_name = flow.get_flow_type()
            _require_name(flow_name)
            if flow_name in flow_names:
                raise ValueError(f"duplicate Flow {flow_name}")
            flow_names.add(flow_name)
            steps, rpcs = Registry._validate_flow(flow, codec_registry)
            registered_steps.extend(steps)
            registered_rpcs.extend(rpcs)
        return tuple(registered_steps), tuple(registered_rpcs)

    @staticmethod
    def _validate_flow(
        flow: Flow[Any], codec_registry: CodecRegistry
    ) -> tuple[list[_RegisteredStep], list[_RegisteredRPC]]:
        definitions = flow.get_steps()
        if not isinstance(definitions, StepList):
            raise TypeError("Flow steps must be a StepList")
        step_names: set[str] = set()
        registered_steps: list[_RegisteredStep] = []
        has_start_step = False
        for definition in definitions:
            if not isinstance(definition, _StepDef):
                raise TypeError("Flow StepList contains an invalid definition")
            if definition.is_start_step:
                if has_start_step:
                    raise ValueError("Flow must not have multiple start Steps")
                has_start_step = True
            step = definition.step
            step_name = step.get_step_type()
            _require_name(step_name)
            if step_name in step_names:
                raise ValueError(f"duplicate Step {step_name}")
            step_names.add(step_name)
            registered_steps.append(
                _RegisteredStep(step, Registry._step_input_codec(step, codec_registry))
            )

        schema = flow.get_persistence_schema()
        registered_rpcs: list[_RegisteredRPC] = []
        rpc_names: set[str] = set()
        for attribute_name in dir(flow):
            method = getattr(flow, attribute_name)
            function = getattr(method, "__func__", method)
            options = getattr(function, "__dex_rpc_options__", None)
            if not isinstance(options, _RPCOptions):
                continue
            rpc_name = options.name or attribute_name
            if rpc_name in rpc_names:
                raise ValueError(f"duplicate RPC {rpc_name}")
            rpc_names.add(rpc_name)
            if any(
                all(lock.attribute is not attribute for attribute in schema.attributes)
                for lock in options.lock_attributes
            ):
                raise ValueError(f"RPC {rpc_name} locks an unregistered attribute")
            registered_rpcs.append(Registry._rpc_codecs(method, codec_registry))
        return registered_steps, registered_rpcs

    @staticmethod
    def _step_input_codec(step: Step[Any], codec_registry: CodecRegistry) -> Codec[Any]:
        parameters = tuple(signature(step.execute).parameters.values())
        hints = get_type_hints(step.execute)
        if len(parameters) != 2 or "input" not in hints:
            raise TypeError(
                f"Step {step.get_step_type()} execute must annotate context and input"
            )
        return codec_registry.resolve(hints["input"])

    @staticmethod
    def _rpc_codecs(
        method: Callable[..., Any], codec_registry: CodecRegistry
    ) -> _RegisteredRPC:
        parameters = tuple(signature(method).parameters.values())
        hints = get_type_hints(method)
        if len(parameters) not in (1, 2) or "return" not in hints:
            raise TypeError("RPC must annotate Context, optional input, and return")
        input_codec = (
            codec_registry.resolve(hints[parameters[1].name])
            if len(parameters) == 2
            else None
        )
        return_type = hints["return"]
        output_codec = None
        if get_origin(return_type) is RPCResult:
            arguments = get_args(return_type)
            if len(arguments) != 1:
                raise TypeError("RPCResult must declare one output type")
            output_codec = codec_registry.resolve(arguments[0])
        elif return_type not in (None, type(None)):
            raise TypeError("RPC must return RPCResult[O] or None")
        return _RegisteredRPC(method, input_codec, output_codec)


@dataclass(frozen=True)
class WorkerTarget:
    address: str
    headless: bool = False


class ActiveStepSearchMode(Enum):
    DEFAULT = "default"
    ALL = "all"


class IdReusePolicy(Enum):
    DEFAULT = "default"
    ALLOW_IF_NOT_RUNNING = "allow_if_not_running"
    ALLOW_TERMINATE_IF_RUNNING = "allow_terminate_if_running"
    DISALLOW = "disallow"


class FlowStatus(Enum):
    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"
    CANCELLED = "cancelled"
    TERMINATED = "terminated"
    TIMED_OUT = "timed_out"


@dataclass(frozen=True)
class FlowConfig:
    active_step_search_mode: ActiveStepSearchMode | None = None
    continue_as_new_threshold: int | None = None
    continue_as_new_page_size_bytes: int | None = None
    step_durability: StepDurability | None = None
    worker_target: WorkerTarget | None = None


@dataclass(frozen=True)
class StartFlowOptions:
    timeout: timedelta | None = None
    start_delay: timedelta | None = None
    id_reuse_policy: IdReusePolicy = IdReusePolicy.DEFAULT
    cron_schedule: str | None = None
    retry_policy: RetryPolicy | None = None
    attributes: tuple[InitialAttribute[Any], ...] = ()
    config_override: FlowConfig | None = None
    ignore_already_started: bool = False
    request_id: str | None = None


@dataclass(frozen=True)
class FlowInfo:
    flow_id: str
    run_id: str
    flow_type: str
    status: FlowStatus
    started_at: datetime


@dataclass(frozen=True)
class StepExecutionId:
    step_type: str
    number: int = 0


@dataclass(frozen=True)
class TimerId:
    condition_id: str | None = None
    condition_index: int | None = None

    @staticmethod
    def by_condition_id(condition_id: str) -> TimerId:
        _require_name(condition_id)
        return TimerId(condition_id=condition_id)

    @staticmethod
    def by_condition_index(condition_index: int) -> TimerId:
        return TimerId(condition_index=condition_index)


class ResetType(Enum):
    BEGINNING = "beginning"
    HISTORY_EVENT_ID = "history_event_id"
    HISTORY_EVENT_TIME = "history_event_time"
    STEP_TYPE = "step_type"
    STEP_EXECUTION_ID = "step_execution_id"


@dataclass(frozen=True)
class ResetFlowOptions:
    type: ResetType
    history_event_id: int | None = None
    history_event_time: datetime | None = None
    step_type: str | None = None
    step_execution_id: str | None = None
    reason: str | None = None
    skip_channel_messages_reapply: bool = False
    skip_locking_rpc_reapply: bool = False


class StopType(Enum):
    CANCEL = "cancel"
    TERMINATE = "terminate"
    FAIL = "fail"


@dataclass(frozen=True)
class StopFlowOptions:
    type: StopType = StopType.CANCEL
    reason: str | None = None


@dataclass(frozen=True)
class ClientOptions:
    server_address: str = "localhost:8801"
    worker_target: WorkerTarget | None = None


@dataclass(frozen=True)
class WorkerOptions:
    bind_address: str = ":8803"
    worker_target: WorkerTarget | None = None
    server_address: str = "localhost:8801"


class Client:
    def __init__(
        self,
        registry: Registry,
        blob_cache: BlobCache,
        options: ClientOptions = ClientOptions(),
    ) -> None:
        self.registry = registry
        self.blob_cache = blob_cache
        self.options = options

    def start_flow(
        self,
        flow: Flow[InputT],
        flow_id: str,
        input: InputT,
        options: StartFlowOptions = StartFlowOptions(),
    ) -> str:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    @overload
    def invoke_rpc(
        self,
        rpc_method: Callable[[Context, InputT], RPCResult[OutputT]],
        flow_id: str,
        input: InputT,
        *,
        run_id: str = "",
    ) -> OutputT: ...

    @overload
    def invoke_rpc(
        self,
        rpc_method: Callable[[Context], RPCResult[OutputT]],
        flow_id: str,
        *,
        run_id: str = "",
    ) -> OutputT: ...

    @overload
    def invoke_rpc(
        self,
        rpc_method: Callable[[Context, InputT], None],
        flow_id: str,
        input: InputT,
        *,
        run_id: str = "",
    ) -> None: ...

    @overload
    def invoke_rpc(
        self,
        rpc_method: Callable[[Context], None],
        flow_id: str,
        *,
        run_id: str = "",
    ) -> None: ...

    def invoke_rpc(
        self,
        rpc_method: Callable[..., Any],
        flow_id: str,
        input: object = None,
        *,
        run_id: str = "",
    ) -> Any:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    @overload
    def get_attribute(
        self,
        flow_id: str,
        attribute: Attribute[ValueT],
        *,
        run_id: str = "",
    ) -> ValueT: ...

    @overload
    def get_attribute(
        self,
        flow_id: str,
        attribute: AttributeMap[ValueT],
        instance: str,
        *,
        run_id: str = "",
    ) -> ValueT: ...

    def get_attribute(
        self,
        flow_id: str,
        attribute: Attribute[Any] | AttributeMap[Any],
        instance: str | None = None,
        *,
        run_id: str = "",
    ) -> Any:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    @overload
    def set_attribute(
        self,
        flow_id: str,
        attribute: Attribute[ValueT],
        value: ValueT,
        *,
        run_id: str = "",
    ) -> None: ...

    @overload
    def set_attribute(
        self,
        flow_id: str,
        attribute: AttributeMap[ValueT],
        instance: str,
        value: ValueT,
        *,
        run_id: str = "",
    ) -> None: ...

    def set_attribute(
        self,
        flow_id: str,
        attribute: Attribute[Any] | AttributeMap[Any],
        *args: object,
        run_id: str = "",
        **kwargs: object,
    ) -> None:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    @overload
    def publish(
        self,
        flow_id: str,
        channel: Channel[ValueT],
        *values: ValueT,
        run_id: str = "",
    ) -> None: ...

    @overload
    def publish(
        self,
        flow_id: str,
        channel: ChannelMap[ValueT],
        instance: str,
        *values: ValueT,
        run_id: str = "",
    ) -> None: ...

    def publish(
        self,
        flow_id: str,
        channel: Channel[Any] | ChannelMap[Any],
        *args: object,
        run_id: str = "",
        **kwargs: object,
    ) -> None:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    @overload
    def wait_for_flow(self, flow_id: str) -> None: ...

    @overload
    def wait_for_flow(
        self,
        flow_id: str,
        output_type: type[OutputT],
        timeout: timedelta | None = None,
    ) -> OutputT: ...

    def wait_for_flow(
        self,
        flow_id: str,
        output_type: type[Any] | None = None,
        timeout: timedelta | None = None,
    ) -> Any:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    def stop_flow(
        self,
        flow_id: str,
        options: StopFlowOptions = StopFlowOptions(),
    ) -> None:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    def describe_flow(self, flow_id: str) -> FlowInfo:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    def reset_flow(self, flow_id: str, options: ResetFlowOptions) -> str:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    def skip_timer(
        self,
        flow_id: str,
        step_execution_id: StepExecutionId,
        timer_id: TimerId,
    ) -> None:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    def wait_for_step_completion(
        self,
        flow_id: str,
        step_execution_id: StepExecutionId,
        timeout: timedelta,
    ) -> None:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    def update_flow_config(self, flow_id: str, config: FlowConfig) -> None:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    def close(self) -> None:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")


class Worker:
    def __init__(
        self,
        registry: Registry,
        blob_cache: BlobCache,
        options: WorkerOptions = WorkerOptions(),
    ) -> None:
        self.registry = registry
        self.blob_cache = blob_cache
        self.options = options

    def start(self) -> None:
        raise PhaseNotImplementedError("Worker runtime belongs to a later phase")

    def stop(self) -> None:
        raise PhaseNotImplementedError("Worker runtime belongs to a later phase")


@dataclass(frozen=True)
class HealthInfo:
    condition: str
    hostname: str
    duration_seconds: int


@dataclass(frozen=True)
class SearchFlowEntry:
    flow_id: str
    run_id: str
    flow_type: str
    status: str
    started_at: datetime
    closed_at: datetime | None
    attributes: Mapping[str, Value]
