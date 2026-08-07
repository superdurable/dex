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

from dex._utils import require_name
from dex.attribute import Attribute, AttributeLock, AttributeMap
from dex.channel import Channel, ChannelMap
from dex.codec import Codec, CodecRegistry
from dex.context import Context
from dex.step import Step, StepDecision, StepList, StepMovement, _StepDef
from dex.wait import Wait

OutputT = TypeVar("OutputT")
StartT = TypeVar("StartT")
CallableT = TypeVar("CallableT", bound=Callable[..., Any])
_PersistenceDefinition = (
    Attribute[Any] | AttributeMap[Any] | Channel[Any] | ChannelMap[Any]
)


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

    @staticmethod
    def of(*definitions: _PersistenceDefinition) -> PersistenceSchema:
        attributes: list[Attribute[Any] | AttributeMap[Any]] = []
        channels: list[Channel[Any] | ChannelMap[Any]] = []
        for definition in definitions:
            if isinstance(definition, (Attribute, AttributeMap)):
                attributes.append(definition)
            elif isinstance(definition, (Channel, ChannelMap)):
                channels.append(definition)
            else:
                raise TypeError("unsupported persistence definition")
        return PersistenceSchema(tuple(attributes), tuple(channels))


class Flow(Generic[StartT], ABC):
    def get_flow_type(self) -> str:
        return type(self).__name__

    def get_steps(self) -> StepList[StartT]:
        return StepList.empty()

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()


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
        if not isinstance(schema, PersistenceSchema):
            raise TypeError("Flow persistence schema must be a PersistenceSchema")
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
            Registry._validate_rpc_locks(rpc_name, options, schema)
            registered_rpcs.append(Registry._rpc_codecs(method, codec_registry))
        return registered_steps, registered_rpcs

    @staticmethod
    def _validate_rpc_locks(
        rpc_name: str,
        options: _RPCOptions,
        schema: PersistenceSchema,
    ) -> None:
        lock_identities: set[tuple[int, str | None]] = set()
        for lock in options.lock_attributes:
            if not isinstance(lock, AttributeLock):
                raise TypeError(f"RPC {rpc_name} has an invalid attribute lock")
            if all(lock.attribute is not attribute for attribute in schema.attributes):
                raise ValueError(f"RPC {rpc_name} locks an unregistered attribute")
            if isinstance(lock.attribute, AttributeMap):
                if lock.instance is None:
                    raise ValueError(
                        f"RPC {rpc_name} attribute-map lock needs an instance"
                    )
                require_name(lock.instance)
            elif lock.instance is not None:
                raise ValueError(
                    f"RPC {rpc_name} attribute lock cannot have an instance"
                )
            identity = (id(lock.attribute), lock.instance)
            if identity in lock_identities:
                raise ValueError(f"RPC {rpc_name} has a duplicate attribute lock")
            lock_identities.add(identity)

    @staticmethod
    def _step_input_codec(step: Step[Any], codec_registry: CodecRegistry) -> Codec[Any]:
        input_type = Registry._step_handler_input_type(
            step,
            "execute",
            step.execute,
            StepDecision,
        )
        if type(step).wait_for is not Step.wait_for:
            wait_input_type = Registry._step_handler_input_type(
                step,
                "wait_for",
                step.wait_for,
                Wait,
            )
            if wait_input_type != input_type:
                raise TypeError(
                    f"Step {step.get_step_type()} handlers must use the same input type"
                )
        return codec_registry.resolve(input_type)

    @staticmethod
    def _step_handler_input_type(
        step: Step[Any],
        handler_name: str,
        handler: Callable[..., Any],
        return_type: type[Any],
    ) -> Any:
        parameters = tuple(signature(handler).parameters.values())
        hints = get_type_hints(handler)
        if len(parameters) != 2:
            raise TypeError(
                f"Step {step.get_step_type()} {handler_name} must accept context and input"
            )
        context_parameter, input_parameter = parameters
        if hints.get(context_parameter.name) is not Context:
            raise TypeError(
                f"Step {step.get_step_type()} {handler_name} context must be Context"
            )
        input_type = hints.get(input_parameter.name)
        if input_type is None:
            raise TypeError(
                f"Step {step.get_step_type()} {handler_name} input must be annotated"
            )
        if hints.get("return") is not return_type:
            raise TypeError(
                f"Step {step.get_step_type()} {handler_name} must return "
                f"{return_type.__name__}"
            )
        return input_type

    @staticmethod
    def _rpc_codecs(
        method: Callable[..., Any], codec_registry: CodecRegistry
    ) -> _RegisteredRPC:
        parameters = tuple(signature(method).parameters.values())
        hints = get_type_hints(method)
        if len(parameters) not in (1, 2) or "return" not in hints:
            raise TypeError("RPC must annotate Context, optional input, and return")
        if hints.get(parameters[0].name) is not Context:
            raise TypeError("RPC context must be Context")
        if len(parameters) == 2 and parameters[1].name not in hints:
            raise TypeError("RPC input must be annotated")
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
