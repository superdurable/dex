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
from inspect import iscoroutinefunction, signature
from types import MappingProxyType
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
from urllib.parse import quote

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
    name: str
    step: Step[Any]
    input_codec: Codec[Any]
    starting: bool
    skips_wait_for: bool


@dataclass(frozen=True)
class _RegisteredRPC:
    name: str
    method: Callable[..., Any]
    options: _RPCOptions
    input_codec: Codec[Any] | None
    output_codec: Codec[Any] | None
    locks: tuple[str, ...]


@dataclass(frozen=True)
class _RegisteredFlow:
    name: str
    flow: Flow[Any]
    steps: MappingProxyType[str, _RegisteredStep]
    start_step: _RegisteredStep | None
    rpcs: MappingProxyType[str, _RegisteredRPC]
    persistence: MappingProxyType[str, _PersistenceDefinition]

    def step(self, name: str) -> _RegisteredStep:
        try:
            return self.steps[name]
        except KeyError as error:
            raise ValueError(f"Step is not registered: {name}") from error

    def rpc(self, name: str) -> _RegisteredRPC:
        try:
            return self.rpcs[name]
        except KeyError as error:
            raise ValueError(f"RPC is not registered: {name}") from error


@dataclass(frozen=True)
class Registry:
    flows: tuple[Flow[Any], ...]
    codec_registry: CodecRegistry
    _steps: tuple[_RegisteredStep, ...]
    _rpcs: tuple[_RegisteredRPC, ...]
    _registered_flows: MappingProxyType[str, _RegisteredFlow]

    def __init__(
        self,
        flows: Sequence[Flow[Any]],
        codec_registry: CodecRegistry | None = None,
    ) -> None:
        immutable_flows = tuple(flows)
        resolved_codecs = codec_registry or CodecRegistry()
        registered_flows = self._assemble(immutable_flows, resolved_codecs)
        registered_steps = tuple(
            step
            for registered_flow in registered_flows.values()
            for step in registered_flow.steps.values()
        )
        registered_rpcs = tuple(
            registered_rpc
            for registered_flow in registered_flows.values()
            for registered_rpc in registered_flow.rpcs.values()
        )
        object.__setattr__(self, "flows", immutable_flows)
        object.__setattr__(self, "codec_registry", resolved_codecs)
        object.__setattr__(self, "_steps", registered_steps)
        object.__setattr__(self, "_rpcs", registered_rpcs)
        object.__setattr__(
            self,
            "_registered_flows",
            MappingProxyType(registered_flows),
        )

    @staticmethod
    def _assemble(
        flows: tuple[Flow[Any], ...], codec_registry: CodecRegistry
    ) -> dict[str, _RegisteredFlow]:
        registered_flows: dict[str, _RegisteredFlow] = {}
        for flow in flows:
            if not isinstance(flow, Flow):
                raise TypeError("Flow definition is invalid")
            flow_name = flow.get_flow_type()
            require_name(flow_name)
            if flow_name in registered_flows:
                raise ValueError(f"duplicate Flow {flow_name}")
            registered_flows[flow_name] = Registry._assemble_flow(
                flow_name,
                flow,
                codec_registry,
            )
        return registered_flows

    @staticmethod
    def _assemble_flow(
        flow_name: str,
        flow: Flow[Any],
        codec_registry: CodecRegistry,
    ) -> _RegisteredFlow:
        definitions = flow.get_steps()
        if not isinstance(definitions, StepList):
            raise TypeError("Flow steps must be a StepList")
        registered_steps: dict[str, _RegisteredStep] = {}
        start_step: _RegisteredStep | None = None
        for definition in definitions:
            if not isinstance(definition, _StepDef):
                raise TypeError("Flow StepList contains an invalid definition")
            if definition.is_start_step:
                if start_step is not None:
                    raise ValueError("Flow must not have multiple start Steps")
            step = definition.step
            step_name = step.get_step_type()
            require_name(step_name)
            if step_name in registered_steps:
                raise ValueError(f"duplicate Step {step_name}")
            registered_step = _RegisteredStep(
                step_name,
                step,
                Registry._step_input_codec(step, codec_registry),
                definition.is_start_step,
                type(step).wait_for is Step.wait_for,
            )
            registered_steps[step_name] = registered_step
            if definition.is_start_step:
                start_step = registered_step

        schema = flow.get_persistence_schema()
        if not isinstance(schema, PersistenceSchema):
            raise TypeError("Flow persistence schema must be a PersistenceSchema")
        persistence = Registry._assemble_persistence(schema)
        registered_rpcs: dict[str, _RegisteredRPC] = {}
        for attribute_name in dir(flow):
            method = getattr(flow, attribute_name)
            function = getattr(method, "__func__", method)
            options = getattr(function, "__dex_rpc_options__", None)
            if not isinstance(options, _RPCOptions):
                continue
            rpc_name = options.name or attribute_name
            require_name(rpc_name)
            if rpc_name in registered_rpcs:
                raise ValueError(f"duplicate RPC {rpc_name}")
            Registry._validate_rpc_locks(rpc_name, options, schema)
            input_codec, output_codec = Registry._rpc_codecs(method, codec_registry)
            registered_rpcs[rpc_name] = _RegisteredRPC(
                rpc_name,
                method,
                options,
                input_codec,
                output_codec,
                tuple(
                    Registry._physical_lock(lock) for lock in options.lock_attributes
                ),
            )
        return _RegisteredFlow(
            flow_name,
            flow,
            MappingProxyType(registered_steps),
            start_step,
            MappingProxyType(registered_rpcs),
            MappingProxyType(persistence),
        )

    @staticmethod
    def _assemble_persistence(
        schema: PersistenceSchema,
    ) -> dict[str, _PersistenceDefinition]:
        persistence: dict[str, _PersistenceDefinition] = {}
        for definition in (*schema.attributes, *schema.channels):
            if definition.name in persistence:
                raise ValueError(f"duplicate persistence definition {definition.name}")
            persistence[definition.name] = definition
        return persistence

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
        if iscoroutinefunction(handler):
            raise TypeError(
                f"Step {step.get_step_type()} {handler_name} must be synchronous"
            )
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
    ) -> tuple[Codec[Any] | None, Codec[Any] | None]:
        if iscoroutinefunction(method):
            raise TypeError("RPC must be synchronous")
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
        return input_codec, output_codec

    @staticmethod
    def physical_name(name: str, instance: str) -> str:
        require_name(instance)
        return f"{name}/{quote(instance, safe='')}"

    @staticmethod
    def _physical_lock(lock: AttributeLock) -> str:
        if lock.instance is None:
            return lock.attribute.name
        return Registry.physical_name(lock.attribute.name, lock.instance)

    def _flow_by_type(self, flow_type: str) -> _RegisteredFlow:
        try:
            return self._registered_flows[flow_type]
        except KeyError as error:
            raise ValueError(f"Flow is not registered: {flow_type}") from error

    def _flow_for_instance(self, flow: Flow[Any]) -> _RegisteredFlow:
        registered = self._flow_by_type(flow.get_flow_type())
        if registered.flow is not flow:
            raise ValueError("Flow instance is not registered")
        return registered

    def _rpc_for_method(
        self,
        method: Callable[..., Any],
    ) -> tuple[_RegisteredFlow, _RegisteredRPC]:
        receiver = getattr(method, "__self__", None)
        function = getattr(method, "__func__", method)
        for flow in self._registered_flows.values():
            if receiver is not flow.flow:
                continue
            for registered_rpc in flow.rpcs.values():
                registered_function = getattr(
                    registered_rpc.method,
                    "__func__",
                    registered_rpc.method,
                )
                if registered_function is function:
                    return flow, registered_rpc
        raise ValueError("RPC method is not registered")
