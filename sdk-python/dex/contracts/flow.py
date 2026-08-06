# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from abc import ABC
from dataclasses import dataclass
from datetime import timedelta
from inspect import signature
from typing import (
    Any,
    Callable,
    Generic,
    Sequence,
    TypeVar,
    get_args,
    get_origin,
    get_type_hints,
    overload,
)

from dex.contracts._common import require_name
from dex.contracts.codec import Codec, CodecRegistry
from dex.contracts.state import (
    Attribute,
    AttributeLock,
    AttributeMap,
    Channel,
    ChannelMap,
)
from dex.contracts.step import Step, StepList, StepMovement, _StepDef

OutputT = TypeVar("OutputT")
StartT = TypeVar("StartT")
ValueT = TypeVar("ValueT")
CallableT = TypeVar("CallableT", bound=Callable[..., Any])


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
        require_name(name)
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
            require_name(flow_name)
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
            require_name(step_name)
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
