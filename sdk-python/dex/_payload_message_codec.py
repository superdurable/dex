# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Apply the payload codec to every Value and error text by walking message descriptors."""

from __future__ import annotations

import asyncio
import base64
import binascii
import inspect
from collections.abc import Callable, Iterator, Sequence
from dataclasses import dataclass, field
from typing import Any, TypeVar, cast

from google.protobuf.descriptor import Descriptor, FieldDescriptor
from google.protobuf.message import DecodeError, Message

from dex.attribute import Attribute, AttributeMap
from dex.codec import WireKind
from dex.dexpb import dex_pb2 as pb
from dex.payload_encryption import (
    AesGcmPayloadCodec,
    AsyncPayloadCodec,
    PayloadCodec,
    PayloadContext,
    PayloadEncryptionError,
    PayloadEncryptionOptions,
    PayloadErrorCategory,
    _fresh_control_flow_exception,
    is_valid_payload_key_id,
)
from dex.runtime_errors import ValueMappingError

MessageT = TypeVar("MessageT", bound=Message)
fresh_control_flow_exception = _fresh_control_flow_exception

PAYLOAD_ENVELOPE_ENCODING = "dex/1"
RESERVED_ENCODING_PREFIX = "dex/"
ENCODED_TEXT_PREFIX = "dex-encoded:"
MISSING_PAYLOAD_CODEC_MESSAGE = "value is encrypted; configure a PayloadCodec"
_MAX_METADATA_ENTRIES = 16
_MAX_METADATA_TOTAL_BYTES = 4096
_VALUE_MESSAGE = "dex.Value"
_ENCRYPTABLE_KINDS = frozenset(
    ("string_value", "obj_value", "int_value", "double_value", "bool_value")
)
_PRIMITIVE_KINDS = frozenset(("int_value", "double_value", "bool_value"))
_PRIMITIVE_WIRE_KINDS = frozenset((WireKind.INT64, WireKind.DOUBLE, WireKind.BOOL))

OUTGOING_TEXT_FIELDS = frozenset(
    (
        ("dex.WorkerErrorResponse", "detail"),
        ("dex.WorkerErrorResponse", "stack_trace"),
        ("dex.StopFlowRequest", "reason"),
        ("dex.ResetFlowRequest", "reason"),
        ("dex.CloneForkFlowRequest", "reason"),
    )
)
INCOMING_TEXT_FIELDS = frozenset(
    (
        ("dex.RecoveryErrorInfo", "detail"),
        ("dex.FlowResult", "error_message"),
        ("dex.FlowClosedHistoryEvent", "error_message"),
        ("dex.ServiceErrorResponse", "detail"),
        ("dex.ServiceErrorResponse", "original_worker_error_detail"),
        ("dex.ServiceErrorResponse", "original_worker_error_stack_trace"),
        ("dex.WorkerErrorResponse", "detail"),
        ("dex.WorkerErrorResponse", "stack_trace"),
    )
)
# The Server compares these Values with stored plaintext.
PLAINTEXT_VALUE_FIELDS = frozenset(
    (
        ("dex.ActionPermissionMapping", "equal_values"),
        ("dex.AttributeMatch", "operand"),
        ("dex.LoadBlobRequestEntry", "blob_value"),
    )
)
_FORCE_FAIL_CLOSE_INPUT = ("dex.CloseDecision", "close_input")
WORKER_ERROR_DETAIL_FIELD = ("dex.WorkerErrorResponse", "detail")


@dataclass(frozen=True)
class PayloadScope:
    """The Flow that stores a message's Values, plus the Step execution for SubFlow IDs."""

    context: PayloadContext
    step_execution_id: str = ""

    def sub_flow(self, flow_type: str, index: int) -> PayloadScope:
        flow_id = f"SubFlow:{self.context.flow_id}-{self.step_execution_id}-{index}"
        return PayloadScope(PayloadContext(flow_type, flow_id))


@dataclass
class _PayloadSlot:
    """One Value or text field that the codec rewrites in place."""

    message: Message
    field_name: str
    is_text: bool
    plaintext: bytes = b""
    envelope: pb.PayloadEnvelope | None = None


@dataclass
class _PayloadBatch:
    context: PayloadContext
    slots: list[_PayloadSlot] = field(default_factory=list)


class _PayloadPlan:
    def __init__(self) -> None:
        self._batches: dict[PayloadContext, _PayloadBatch] = {}
        # Text that starts with the prefix and stays plaintext gets a leading space.
        self.escapes: list[tuple[Message, str]] = []

    def add(self, context: PayloadContext, slot: _PayloadSlot) -> None:
        batch = self._batches.get(context)
        if batch is None:
            batch = _PayloadBatch(context)
            self._batches[context] = batch
        batch.slots.append(slot)

    @property
    def batches(self) -> list[_PayloadBatch]:
        return list(self._batches.values())

    @property
    def is_empty(self) -> bool:
        return not self._batches and not self.escapes

    def apply_escapes(self) -> None:
        for message, field_name in self.escapes:
            setattr(message, field_name, " " + getattr(message, field_name))


class PayloadMessageTransformer:
    """Plan and apply codec work; the sync and async drivers only call the codec."""

    def __init__(
        self,
        options: PayloadEncryptionOptions | None,
        registry: Any = None,
        max_encoded_text_bytes: int | None = None,
    ) -> None:
        self.options = options
        self._registry = registry
        # Defaults to the base64 length of max_envelope_bytes; conformance tests lower it.
        self.max_encoded_text_bytes = (
            max_encoded_text_bytes
            if max_encoded_text_bytes is not None
            else 4 * ((options.max_envelope_bytes + 2) // 3) if options else 0
        )

    @property
    def is_enabled(self) -> bool:
        return self.options is not None

    # Rejection rule for Values the Server must compare in plaintext.

    def require_plaintext_attribute(
        self,
        definition: Attribute[Any] | AttributeMap[Any] | None,
        name: str,
        use: str,
    ) -> None:
        if self.options is None or not self.would_encrypt_attribute(definition):
            return
        raise ValueError(
            f"{use} needs Attribute {name} in plaintext, but payload encryption would "
            "encrypt it; give it an index or declare it with plaintext=True"
        )

    def require_plaintext_action_conditions(self) -> None:
        """Reject, at Worker construction, Action conditions on encrypted Attributes."""
        registered_flows = getattr(self._registry, "_registered_flows", {})
        for flow in registered_flows.values():
            for action in flow.actions:
                if action.attribute_name:
                    self.require_plaintext_attribute(
                        self.attribute_definition(flow.name, action.attribute_name),
                        action.attribute_name,
                        f"The Action condition of RPC {action.rpc_name} in Flow {flow.name}",
                    )

    def would_encrypt_attribute(
        self, definition: Attribute[Any] | AttributeMap[Any] | None
    ) -> bool:
        options = self.options
        if options is None:
            return False
        if definition is None:
            return True
        if definition.index is not None or definition.plaintext:
            return False
        wire_kind = self._wire_kind(definition)
        if wire_kind is WireKind.STRING:
            return options.encrypt_string_values
        if wire_kind in _PRIMITIVE_WIRE_KINDS:
            return options.encrypt_primitive_values
        return True

    def registered_attribute(
        self, definition: Attribute[Any] | AttributeMap[Any]
    ) -> Attribute[Any] | AttributeMap[Any] | None:
        """Return ``definition`` when some registered Flow declares it, else ``None``."""
        registered_flows = getattr(self._registry, "_registered_flows", {})
        for flow in registered_flows.values():
            if flow.persistence.get(definition.name) == definition:
                return definition
        return None

    def attribute_definition(
        self, flow_type: str, key: str
    ) -> Attribute[Any] | AttributeMap[Any] | None:
        registered_flows = getattr(self._registry, "_registered_flows", None)
        if registered_flows is None:
            return None
        flow = registered_flows.get(flow_type)
        if flow is None:
            return None
        definition = flow.persistence.get(key)
        if isinstance(definition, Attribute):
            return definition
        name, separator, _ = key.partition("/")
        if separator:
            definition = flow.persistence.get(name)
            if isinstance(definition, AttributeMap):
                return definition
        return None

    def _wire_kind(self, definition: Attribute[Any] | AttributeMap[Any]) -> WireKind:
        codecs = getattr(self._registry, "codec_registry", None)
        if codecs is None:
            from dex.codec import CodecRegistry

            codecs = CodecRegistry()
        return cast(WireKind, codecs.resolve(definition.value_type).wire_kind)

    # Message walking.

    def prepare_encoding(
        self,
        message: MessageT,
        scope: PayloadScope,
        plaintext_text_fields: frozenset[tuple[str, str]],
    ) -> tuple[MessageT, _PayloadPlan] | None:
        """Return a copy and its plan, or ``None`` when the message needs no change."""
        if not self.might_encode(message):
            return None
        if (
            self.options is None
            and self.plan_encoding(message, scope, plaintext_text_fields).is_empty
        ):
            return None
        encoded = _copy_message(message)
        plan = self.plan_encoding(encoded, scope, plaintext_text_fields)
        plan.apply_escapes()
        return encoded, plan

    def prepare_decoding(
        self, message: MessageT, scope: PayloadScope, include_values: bool
    ) -> tuple[MessageT, _PayloadPlan] | None:
        """Return a copy and its plan, or ``None`` when nothing is encrypted."""
        if not self.might_decode(message, include_values):
            return None
        if self.plan_decoding(message, scope, include_values).is_empty:
            return None
        decoded = _copy_message(message)
        return decoded, self.plan_decoding(decoded, scope, include_values)

    def prepare_value_decoding(
        self, values: Sequence[pb.Value], context: PayloadContext
    ) -> tuple[list[pb.Value], _PayloadPlan] | None:
        if self.plan_value_decoding(values, context).is_empty:
            return None
        decoded = [_copy_message(value) for value in values]
        return decoded, self.plan_value_decoding(decoded, context)

    def _encoding_reach(self) -> frozenset[str]:
        if self.options is None:
            return _ENCODING_REACH_WITHOUT_CODEC
        return _ENCODING_REACH_WITH_CODEC

    def _decoding_reach(self, include_values: bool) -> frozenset[str]:
        if include_values:
            return _DECODING_REACH_WITH_VALUES
        return _DECODING_REACH_TEXT_ONLY

    def might_encode(self, message: Message) -> bool:
        return _reaches(message.DESCRIPTOR, self._encoding_reach())

    def might_decode(self, message: Message, include_values: bool) -> bool:
        return _reaches(message.DESCRIPTOR, self._decoding_reach(include_values))

    def _walk(
        self,
        message: Message,
        scope: PayloadScope,
        targets: frozenset[str],
        visitor: _MessageVisitor,
    ) -> None:
        name = message.DESCRIPTOR.full_name
        scope = visitor.enter(message, scope)
        for descriptor, value in message.ListFields():
            if descriptor.type != FieldDescriptor.TYPE_MESSAGE:
                if descriptor.type == FieldDescriptor.TYPE_STRING:
                    visitor.text(message, descriptor.name, scope)
                continue
            item_type = _item_message_type(descriptor)
            if item_type is None or not _reaches(item_type, targets):
                continue
            for index, item in enumerate(_message_items(descriptor, value)):
                if item_type.full_name == _VALUE_MESSAGE:
                    visitor.value(message, descriptor.name, cast(pb.Value, item), scope)
                    continue
                child_scope = visitor.child_scope(name, descriptor.name, index, scope)
                self._walk(item, child_scope, targets, visitor)

    # Encoding.

    def plan_encoding(
        self,
        message: Message,
        scope: PayloadScope,
        plaintext_text_fields: frozenset[tuple[str, str]] = frozenset(),
    ) -> _PayloadPlan:
        """Plan encryption; without a codec, only escape prefix-colliding text."""
        visitor = _EncodingVisitor(self, plaintext_text_fields)
        self._walk(message, scope, self._encoding_reach(), visitor)
        return visitor.plan

    def apply_envelopes(
        self, batch: _PayloadBatch, envelopes: list[pb.PayloadEnvelope]
    ) -> None:
        """Write SDK-owned envelope copies; encode errors never echo a codec key ID."""
        options = cast(PayloadEncryptionOptions, self.options)
        for slot, envelope in zip(batch.slots, envelopes, strict=True):
            _check_envelope_header("encode", envelope, report_key_id=False)
            if envelope.cipher == pb.PAYLOAD_CIPHER_UNSPECIFIED:
                raise PayloadEncryptionError(
                    "encode",
                    PayloadErrorCategory.UNSUPPORTED_CIPHER,
                    "codec returned an envelope without a cipher",
                )
            serialized = envelope.SerializeToString()
            if len(serialized) > options.max_envelope_bytes:
                raise PayloadEncryptionError(
                    "encode",
                    PayloadErrorCategory.LIMIT_EXCEEDED,
                    "envelope exceeds max_envelope_bytes",
                )
            if slot.is_text:
                encoded = base64.b64encode(serialized).decode("ascii")
                setattr(slot.message, slot.field_name, ENCODED_TEXT_PREFIX + encoded)
            else:
                cast(pb.Value, slot.message).CopyFrom(
                    pb.Value(
                        obj_value=pb.EncodedObject(
                            encoding=PAYLOAD_ENVELOPE_ENCODING, payload=serialized
                        )
                    )
                )

    def _plaintext(self, value: pb.Value) -> bytes:
        options = cast(PayloadEncryptionOptions, self.options)
        plaintext = value.SerializeToString()
        if len(plaintext) > options.max_plaintext_bytes:
            raise PayloadEncryptionError(
                "encode",
                PayloadErrorCategory.LIMIT_EXCEEDED,
                "plaintext exceeds max_plaintext_bytes",
            )
        return plaintext

    # Decoding.

    def plan_decoding(
        self, message: Message, scope: PayloadScope, include_values: bool = True
    ) -> _PayloadPlan:
        visitor = _DecodingVisitor(self, include_values)
        self._walk(message, scope, self._decoding_reach(include_values), visitor)
        return visitor.plan

    def plan_value_decoding(
        self, values: Sequence[pb.Value], context: PayloadContext
    ) -> _PayloadPlan:
        visitor = _DecodingVisitor(self, True)
        scope = PayloadScope(context)
        for value in values:
            visitor.add_value(value, scope)
        return visitor.plan

    def parse_envelope_value(self, value: pb.Value) -> pb.PayloadEnvelope | None:
        """Return the envelope of a marked Value, ``None`` for other Values."""
        if value.WhichOneof("kind") != "obj_value":
            return None
        encoding = value.obj_value.encoding
        if not encoding.startswith(RESERVED_ENCODING_PREFIX):
            return None
        if self.options is None:
            raise ValueMappingError("decode", MISSING_PAYLOAD_CODEC_MESSAGE)
        if encoding != PAYLOAD_ENVELOPE_ENCODING:
            raise PayloadEncryptionError(
                "decode",
                PayloadErrorCategory.UNSUPPORTED_MARKER,
                "the encoding is not dex/1; a newer SDK may have written it",
            )
        payload = value.obj_value.payload
        if len(payload) > self.options.max_envelope_bytes:
            raise PayloadEncryptionError(
                "decode", PayloadErrorCategory.LIMIT_EXCEEDED, "envelope is too large"
            )
        return _parse_envelope(payload)

    def parse_envelope_text(self, text: str) -> pb.PayloadEnvelope | None:
        """Return the envelope of prefixed text, ``None`` for other text."""
        if not text.startswith(ENCODED_TEXT_PREFIX):
            return None
        if self.options is None:
            raise ValueMappingError("decode", MISSING_PAYLOAD_CODEC_MESSAGE)
        encoded = text[len(ENCODED_TEXT_PREFIX) :]
        max_envelope_bytes = self.options.max_envelope_bytes
        if len(encoded) > self.max_encoded_text_bytes:
            raise PayloadEncryptionError(
                "decode",
                PayloadErrorCategory.LIMIT_EXCEEDED,
                "encoded text is too large",
            )
        payload: bytes | None = None
        try:
            payload = base64.b64decode(encoded, validate=True)
        except (binascii.Error, ValueError):
            payload = None
        if payload is None:
            raise PayloadEncryptionError(
                "decode", PayloadErrorCategory.MALFORMED, "text is not base64"
            )
        if base64.b64encode(payload).decode("ascii") != encoded:
            raise PayloadEncryptionError(
                "decode",
                PayloadErrorCategory.MALFORMED,
                "text is not canonical padded base64",
            )
        if len(payload) > max_envelope_bytes:
            raise PayloadEncryptionError(
                "decode", PayloadErrorCategory.LIMIT_EXCEEDED, "envelope is too large"
            )
        return _parse_envelope(payload)

    def apply_plaintexts(self, batch: _PayloadBatch, plaintexts: list[bytes]) -> None:
        """Write SDK-owned plaintexts; key IDs come from the SDK's own envelopes."""
        options = cast(PayloadEncryptionOptions, self.options)
        for slot, plaintext in zip(batch.slots, plaintexts, strict=True):
            key_id = slot.envelope.key_id if slot.envelope is not None else ""
            if len(plaintext) > options.max_plaintext_bytes:
                raise PayloadEncryptionError(
                    "decode",
                    PayloadErrorCategory.LIMIT_EXCEEDED,
                    "plaintext exceeds max_plaintext_bytes",
                    key_id,
                )
            value = parse_encrypted_plaintext(plaintext, slot.is_text, key_id)
            if slot.is_text:
                setattr(slot.message, slot.field_name, value.string_value)
            else:
                cast(pb.Value, slot.message).CopyFrom(value)


class _MessageVisitor:
    def __init__(self, transformer: PayloadMessageTransformer) -> None:
        self.transformer = transformer
        self.plan = _PayloadPlan()

    def enter(self, message: Message, scope: PayloadScope) -> PayloadScope:
        return scope

    def child_scope(
        self, parent: str, field_name: str, index: int, scope: PayloadScope
    ) -> PayloadScope:
        return scope

    def value(
        self, parent: Message, field_name: str, value: pb.Value, scope: PayloadScope
    ) -> None:
        raise NotImplementedError

    def text(self, message: Message, field_name: str, scope: PayloadScope) -> None:
        raise NotImplementedError


class _EncodingVisitor(_MessageVisitor):
    def __init__(
        self,
        transformer: PayloadMessageTransformer,
        plaintext_text_fields: frozenset[tuple[str, str]],
    ) -> None:
        super().__init__(transformer)
        self._plaintext_text_fields = plaintext_text_fields

    def enter(self, message: Message, scope: PayloadScope) -> PayloadScope:
        name = message.DESCRIPTOR.full_name
        transformer = self.transformer
        if name == "dex.SubFlowCondition":
            condition = cast(pb.SubFlowCondition, message)
            return scope.sub_flow(condition.sub_flow_type, condition.sub_flow_index)
        if name == "dex.ActionPermissionMapping" and transformer.is_enabled:
            mapping = cast(pb.ActionPermissionMapping, message)
            transformer.require_plaintext_attribute(
                transformer.attribute_definition(
                    scope.context.flow_type, mapping.attribute_key
                ),
                mapping.attribute_key,
                "An Action condition",
            )
        return scope

    def value(
        self, parent: Message, field_name: str, value: pb.Value, scope: PayloadScope
    ) -> None:
        field_key = (parent.DESCRIPTOR.full_name, field_name)
        if field_key == _FORCE_FAIL_CLOSE_INPUT:
            close = cast(pb.CloseDecision, parent)
            if close.close_decision_type == pb.CLOSE_DECISION_TYPE_FORCE_FAIL:
                if value.WhichOneof("kind") == "string_value":
                    self._add_text(value, "string_value", value.string_value, scope)
                return
        options = self.transformer.options
        if options is None or field_key in PLAINTEXT_VALUE_FIELDS:
            return
        if field_key == ("dex.AttributeWrite", "value") and self._is_plaintext_write(
            cast(pb.AttributeWrite, parent), scope
        ):
            return
        kind = value.WhichOneof("kind")
        if kind == "obj_value" and value.obj_value.encoding.startswith(
            RESERVED_ENCODING_PREFIX
        ):
            raise PayloadEncryptionError(
                "encode",
                PayloadErrorCategory.INVALID_PLAINTEXT,
                "object encodings that start with dex/ are reserved for Dex",
            )
        if kind not in _ENCRYPTABLE_KINDS:
            return
        if kind == "string_value" and not options.encrypt_string_values:
            return
        if kind in _PRIMITIVE_KINDS and not options.encrypt_primitive_values:
            return
        slot = _PayloadSlot(value, "", False, self.transformer._plaintext(value))
        self.plan.add(scope.context, slot)

    def text(self, message: Message, field_name: str, scope: PayloadScope) -> None:
        field_key = (message.DESCRIPTOR.full_name, field_name)
        if field_key not in OUTGOING_TEXT_FIELDS:
            return
        self._add_text(message, field_name, getattr(message, field_name), scope)

    def _add_text(
        self, message: Message, field_name: str, text: str, scope: PayloadScope
    ) -> None:
        options = self.transformer.options
        collides = text.startswith(ENCODED_TEXT_PREFIX)
        field_key = (message.DESCRIPTOR.full_name, field_name)
        keep_plaintext = field_key in self._plaintext_text_fields
        if options is None or (keep_plaintext and collides):
            if collides:
                self.plan.escapes.append((message, field_name))
            return
        if keep_plaintext or not (options.encrypt_error_text or collides):
            return
        if not text:
            return
        plaintext = self.transformer._plaintext(pb.Value(string_value=text))
        self.plan.add(scope.context, _PayloadSlot(message, field_name, True, plaintext))

    def _is_plaintext_write(
        self, write: pb.AttributeWrite, scope: PayloadScope
    ) -> bool:
        if write.index_config.enable:
            return True
        definition = self.transformer.attribute_definition(
            scope.context.flow_type, write.key
        )
        return definition is not None and definition.plaintext


class _DecodingVisitor(_MessageVisitor):
    def __init__(
        self, transformer: PayloadMessageTransformer, include_values: bool
    ) -> None:
        super().__init__(transformer)
        self._include_values = include_values

    def enter(self, message: Message, scope: PayloadScope) -> PayloadScope:
        if message.DESCRIPTOR.full_name == "dex.SearchFlowsResponseEntry":
            entry = cast(pb.SearchFlowsResponseEntry, message)
            return PayloadScope(PayloadContext(entry.flow_type, entry.flow_id))
        return scope

    def child_scope(
        self, parent: str, field_name: str, index: int, scope: PayloadScope
    ) -> PayloadScope:
        if (parent, field_name) == ("dex.ConditionResults", "sub_flow_results"):
            return scope.sub_flow("", index)
        return scope

    def value(
        self, parent: Message, field_name: str, value: pb.Value, scope: PayloadScope
    ) -> None:
        if self._include_values:
            self.add_value(value, scope)

    def add_value(self, value: pb.Value, scope: PayloadScope) -> None:
        envelope = self.transformer.parse_envelope_value(value)
        if envelope is not None:
            slot = _PayloadSlot(value, "", False, envelope=envelope)
            self.plan.add(scope.context, slot)

    def text(self, message: Message, field_name: str, scope: PayloadScope) -> None:
        if (message.DESCRIPTOR.full_name, field_name) not in INCOMING_TEXT_FIELDS:
            return
        envelope = self.transformer.parse_envelope_text(getattr(message, field_name))
        if envelope is not None:
            slot = _PayloadSlot(message, field_name, True, envelope=envelope)
            self.plan.add(scope.context, slot)


class PayloadMessageCodec:
    """Run the codec synchronously for the sync Client, Worker, and hydrator."""

    def __init__(
        self,
        options: PayloadEncryptionOptions | None,
        registry: Any = None,
        max_encoded_text_bytes: int | None = None,
    ) -> None:
        if options is not None and _is_async_codec(options.codec):
            raise TypeError(
                "Client and Worker need a synchronous PayloadCodec; use AsyncClient "
                "or AsyncWorker with an AsyncPayloadCodec"
            )
        self.transformer = PayloadMessageTransformer(
            options, registry, max_encoded_text_bytes
        )
        self._codec = cast(PayloadCodec | None, options.codec if options else None)
        self._decode_arguments = _decode_keyword_arguments(options)

    def _call_codec(
        self, operation: str, context: PayloadContext, items: Sequence[Any]
    ) -> list[Any]:
        """Call the codec and copy its result; nothing codec-owned leaves here."""
        failure: BaseException | None = None
        codec_items = _codec_owned_copies(items)
        try:
            method = getattr(self._codec, operation)
            arguments = self._decode_arguments if operation == "decode" else {}
            result = method(context, codec_items, **arguments)
            if inspect.iscoroutine(result):
                result.close()
                raise TypeError("a synchronous PayloadCodec returned a coroutine")
            return _copy_codec_result(operation, result, len(items))
        except BaseException as error:
            failure = _codec_boundary_failure(operation, self._codec, error, items)
        # Raised outside the handler so the codec's exception is never chained.
        raise failure

    def encode_message(
        self,
        message: MessageT,
        scope: PayloadScope,
        plaintext_text_fields: frozenset[tuple[str, str]] = frozenset(),
    ) -> MessageT:
        prepared = self.transformer.prepare_encoding(
            message, scope, plaintext_text_fields
        )
        if prepared is None:
            return message
        encoded, plan = prepared
        for batch in plan.batches:
            envelopes = self._call_codec(
                "encode", batch.context, [s.plaintext for s in batch.slots]
            )
            self.transformer.apply_envelopes(batch, envelopes)
        return encoded

    def decode_message(
        self, message: MessageT, scope: PayloadScope, include_values: bool = True
    ) -> MessageT:
        prepared = self.transformer.prepare_decoding(message, scope, include_values)
        if prepared is None:
            return message
        decoded, plan = prepared
        self._run_decoding(plan)
        return decoded

    def decode_values(
        self, values: Sequence[pb.Value], context: PayloadContext
    ) -> list[pb.Value]:
        prepared = self.transformer.prepare_value_decoding(values, context)
        if prepared is None:
            return list(values)
        decoded, plan = prepared
        self._run_decoding(plan)
        return decoded

    def decode_text(self, text: str, context: PayloadContext) -> str:
        holder = pb.RecoveryErrorInfo(detail=text)
        return self.decode_message(holder, PayloadScope(context)).detail

    def _run_decoding(self, plan: _PayloadPlan) -> None:
        for batch in plan.batches:
            plaintexts = self._call_codec(
                "decode",
                batch.context,
                [cast(pb.PayloadEnvelope, s.envelope) for s in batch.slots],
            )
            self.transformer.apply_plaintexts(batch, plaintexts)


class AsyncPayloadMessageCodec:
    """Run the codec without blocking the event loop."""

    def __init__(
        self,
        options: PayloadEncryptionOptions | None,
        registry: Any = None,
    ) -> None:
        self.transformer = PayloadMessageTransformer(options, registry)
        self._codec: PayloadCodec | AsyncPayloadCodec | None = (
            options.codec if options else None
        )
        self._is_async = self._codec is not None and _is_async_codec(self._codec)
        self._decode_arguments = _decode_keyword_arguments(options)

    async def encode_message(
        self,
        message: MessageT,
        scope: PayloadScope,
        plaintext_text_fields: frozenset[tuple[str, str]] = frozenset(),
    ) -> MessageT:
        prepared = self.transformer.prepare_encoding(
            message, scope, plaintext_text_fields
        )
        if prepared is None:
            return message
        encoded, plan = prepared
        for batch in plan.batches:
            plaintexts = [slot.plaintext for slot in batch.slots]
            envelopes = await self._call("encode", batch.context, plaintexts)
            self.transformer.apply_envelopes(batch, envelopes)
        return encoded

    async def decode_message(
        self, message: MessageT, scope: PayloadScope, include_values: bool = True
    ) -> MessageT:
        prepared = self.transformer.prepare_decoding(message, scope, include_values)
        if prepared is None:
            return message
        decoded, plan = prepared
        await self._run_decoding(plan)
        return decoded

    async def decode_values(
        self, values: Sequence[pb.Value], context: PayloadContext
    ) -> list[pb.Value]:
        prepared = self.transformer.prepare_value_decoding(values, context)
        if prepared is None:
            return list(values)
        decoded, plan = prepared
        await self._run_decoding(plan)
        return decoded

    async def decode_text(self, text: str, context: PayloadContext) -> str:
        holder = pb.RecoveryErrorInfo(detail=text)
        return (await self.decode_message(holder, PayloadScope(context))).detail

    async def _run_decoding(self, plan: _PayloadPlan) -> None:
        for batch in plan.batches:
            envelopes = [
                cast(pb.PayloadEnvelope, slot.envelope) for slot in batch.slots
            ]
            plaintexts = await self._call("decode", batch.context, envelopes)
            self.transformer.apply_plaintexts(batch, plaintexts)

    async def _call(
        self, operation: str, context: PayloadContext, items: Sequence[Any]
    ) -> list[Any]:
        """Call the codec and copy its result; nothing codec-owned leaves here."""
        failure: BaseException | None = None
        codec_items = _codec_owned_copies(items)
        try:
            method: Callable[..., Any] = getattr(self._codec, operation)
            arguments = self._decode_arguments if operation == "decode" else {}
            if self._is_async:
                result = method(context, codec_items, **arguments)
            else:
                result = await asyncio.to_thread(
                    method, context, codec_items, **arguments
                )
            # Covers async callables that reflection did not recognize.
            if inspect.isawaitable(result):
                result = await result
            return _copy_codec_result(operation, result, len(items))
        except BaseException as error:
            failure = _codec_boundary_failure(operation, self._codec, error, items)
        # Raised outside the handler so the codec's exception is never chained.
        raise failure


def parse_encrypted_plaintext(plaintext: bytes, is_text: bool, key_id: str) -> pb.Value:
    """Parse and check a decrypted plaintext (format step 14)."""
    value = pb.Value()
    if not _parse_without_raising(value, plaintext):
        raise PayloadEncryptionError(
            "decode",
            PayloadErrorCategory.INVALID_PLAINTEXT,
            "plaintext is not a Value",
            key_id,
        )
    kind = value.WhichOneof("kind")
    is_valid = kind in _ENCRYPTABLE_KINDS and not (
        kind == "obj_value"
        and value.obj_value.encoding.startswith(RESERVED_ENCODING_PREFIX)
    )
    if is_text:
        is_valid = kind == "string_value"
    if not is_valid:
        raise PayloadEncryptionError(
            "decode",
            PayloadErrorCategory.INVALID_PLAINTEXT,
            f"plaintext holds a Value kind that is never encrypted here ({kind})",
            key_id,
        )
    return value


def _parse_envelope(payload: bytes) -> pb.PayloadEnvelope:
    envelope = pb.PayloadEnvelope()
    if not _parse_without_raising(envelope, payload):
        raise PayloadEncryptionError(
            "decode", PayloadErrorCategory.MALFORMED, "payload is not a PayloadEnvelope"
        )
    _check_envelope_header("decode", envelope)
    if envelope.cipher == pb.PAYLOAD_CIPHER_UNSPECIFIED:
        raise PayloadEncryptionError(
            "decode",
            PayloadErrorCategory.UNSUPPORTED_CIPHER,
            "cipher is unspecified",
            envelope.key_id,
        )
    return envelope


def _check_envelope_header(
    operation: str, envelope: pb.PayloadEnvelope, report_key_id: bool = True
) -> None:
    if not is_valid_payload_key_id(envelope.key_id):
        raise PayloadEncryptionError(
            operation,
            PayloadErrorCategory.MALFORMED,
            "key id must be 1 to 64 printable ASCII bytes",
        )
    metadata = envelope.metadata
    total = sum(len(key.encode()) + len(value) for key, value in metadata.items())
    if len(metadata) > _MAX_METADATA_ENTRIES or total > _MAX_METADATA_TOTAL_BYTES:
        raise PayloadEncryptionError(
            operation,
            PayloadErrorCategory.LIMIT_EXCEEDED,
            "metadata exceeds 16 entries or 4096 bytes",
            envelope.key_id if report_key_id else "",
        )


def _decode_keyword_arguments(
    options: PayloadEncryptionOptions | None,
) -> dict[str, int]:
    """Pass ``max_plaintext_bytes`` only to a ``decode`` that declares it.

    Unreadable signatures pass nothing; the SDK still checks returned sizes.
    """
    if options is None:
        return {}
    codec = options.codec
    accepts_limit = _reflect_codec(lambda: _declares_plaintext_limit(codec), False)
    return {"max_plaintext_bytes": options.max_plaintext_bytes} if accepts_limit else {}


def _declares_plaintext_limit(codec: object) -> bool:
    parameters = inspect.signature(getattr(codec, "decode")).parameters.values()
    return any(
        parameter.kind is inspect.Parameter.VAR_KEYWORD
        or (
            parameter.name == "max_plaintext_bytes"
            and parameter.kind
            in (inspect.Parameter.KEYWORD_ONLY, inspect.Parameter.POSITIONAL_OR_KEYWORD)
        )
        for parameter in parameters
    )


def _reflect_codec(read: Callable[[], bool], default: bool) -> bool:
    """Run reflection on application objects without letting their exceptions out."""
    control_flow: BaseException | None = None
    try:
        return read()
    except BaseException as error:
        control_flow = fresh_control_flow_exception(error)
    if control_flow is not None:
        raise control_flow
    return default


def _copy_codec_result(operation: str, result: object, expected: int) -> list[Any]:
    """Copy a codec result into SDK-owned values; call only inside the boundary."""
    items = list(cast(Any, result))
    if len(items) != expected:
        raise PayloadEncryptionError(
            operation,
            PayloadErrorCategory.MALFORMED,
            "codec returned a different number of results",
        )
    copies: list[Any] = []
    for item in items:
        if operation == "encode" and type(item) is pb.PayloadEnvelope:
            copies.append(_copy_message(item))
        elif operation == "decode" and type(item) is bytes:
            copies.append(item)
        else:
            raise PayloadEncryptionError(
                operation,
                PayloadErrorCategory.MALFORMED,
                "codec returned a result of the wrong type",
            )
    return copies


def _codec_boundary_failure(
    operation: str, codec: object, error: BaseException, items: Sequence[Any]
) -> BaseException:
    """Return a fresh control-flow exception, or the rebuilt payload error."""
    control_flow = fresh_control_flow_exception(error)
    if control_flow is not None:
        return control_flow
    return _codec_boundary_error(operation, codec, error, items)


def _parse_without_raising(message: Message, payload: bytes) -> bool:
    """Parse ``payload``; callers raise outside a handler so errors carry no context."""
    try:
        message.ParseFromString(payload)
    except DecodeError:
        return False
    return True


def _codec_owned_copies(items: Sequence[Any]) -> list[Any]:
    """Give the codec copies, so the SDK's envelopes and key IDs stay its own."""
    return [
        _copy_message(item) if type(item) is pb.PayloadEnvelope else item
        for item in items
    ]


def _codec_boundary_error(
    operation: str, codec: object, error: object, items: Sequence[Any]
) -> PayloadEncryptionError:
    """Rebuild any codec exception from validated fields only, never reusing it."""
    envelope_key_ids = frozenset(
        item.key_id for item in items if type(item) is pb.PayloadEnvelope
    )
    category, key_id = untrusted_payload_error_fields(error, envelope_key_ids)
    detail = (
        "the AES-256-GCM codec rejected the payload"
        if type(codec) is AesGcmPayloadCodec
        else "the payload codec failed"
    )
    return PayloadEncryptionError(operation, category, detail, key_id)


def untrusted_payload_error_fields(
    error: object, allowed_key_ids: frozenset[str] = frozenset()
) -> tuple[PayloadErrorCategory, str]:
    """Return the category and allowed key ID of an untrusted error without raising.

    Only exact types are trusted, so overridden getters and ``__str__`` never run
    past one guarded attribute read.
    """
    category = PayloadErrorCategory.CODEC_FAILED
    key_id = ""
    if not issubclass(type(error), PayloadEncryptionError):
        return category, key_id
    reported_category = _read_attribute_without_raising(error, "category")
    if type(reported_category) is PayloadErrorCategory:
        category = reported_category
    reported_key_id = _read_attribute_without_raising(error, "key_id")
    if (
        type(reported_key_id) is str
        and reported_key_id in allowed_key_ids
        and is_valid_payload_key_id(reported_key_id)
    ):
        key_id = reported_key_id
    return category, key_id


def _read_attribute_without_raising(target: object, name: str) -> object:
    # Any exception, control flow included, means the field is absent.
    try:
        return getattr(target, name)
    except BaseException:
        return None


def _is_async_codec(codec: object) -> bool:
    return _reflect_codec(
        lambda: any(
            _is_coroutine_callable(getattr(codec, name, None))
            for name in ("encode", "decode")
        ),
        False,
    )


def _is_coroutine_callable(target: object) -> bool:
    return inspect.iscoroutinefunction(target) or inspect.iscoroutinefunction(
        getattr(target, "__call__", None)
    )


def _item_message_type(descriptor: FieldDescriptor) -> Descriptor | None:
    message_type = descriptor.message_type
    if message_type is not None and message_type.GetOptions().map_entry:
        return message_type.fields_by_name["value"].message_type
    return message_type


def _message_items(descriptor: FieldDescriptor, value: Any) -> Iterator[Message]:
    message_type = descriptor.message_type
    if message_type is not None and message_type.GetOptions().map_entry:
        return iter([value[key] for key in sorted(value)])
    if descriptor.is_repeated:
        return iter(value)
    return iter([value])


def _copy_message(message: MessageT) -> MessageT:
    copied = type(message)()
    copied.CopyFrom(message)
    return copied


def _protocol_message_descriptors() -> list[Any]:
    found: dict[str, Any] = {}
    pending: list[Any] = list(pb.DESCRIPTOR.message_types_by_name.values())
    while pending:
        descriptor = pending.pop()
        if descriptor.full_name in found:
            continue
        found[descriptor.full_name] = descriptor
        pending.extend(descriptor.nested_types)
        pending.extend(
            field.message_type
            for field in descriptor.fields
            if field.message_type is not None
        )
    return list(found.values())


_PROTOCOL_MESSAGES = _protocol_message_descriptors()
_KNOWN_MESSAGE_NAMES = frozenset(d.full_name for d in _PROTOCOL_MESSAGES)


def _messages_reaching(targets: frozenset[str]) -> frozenset[str]:
    """Return every message type that contains a target type, as a fixed point."""
    reaching = {d.full_name for d in _PROTOCOL_MESSAGES if d.full_name in targets}
    is_changed = True
    while is_changed:
        is_changed = False
        for descriptor in _PROTOCOL_MESSAGES:
            if descriptor.full_name in reaching:
                continue
            if any(
                field.message_type is not None
                and field.message_type.full_name in reaching
                for field in descriptor.fields
            ):
                reaching.add(descriptor.full_name)
                is_changed = True
    return frozenset(reaching)


def _reaches(descriptor: Any, reaching: frozenset[str]) -> bool:
    # Unknown message types are walked, so an unlisted type fails closed.
    name = descriptor.full_name
    if name in _KNOWN_MESSAGE_NAMES:
        return name in reaching
    return not name.startswith("google.protobuf.")


_OUTGOING_TEXT_MESSAGES = frozenset(name for name, _ in OUTGOING_TEXT_FIELDS)
_INCOMING_TEXT_MESSAGES = frozenset(name for name, _ in INCOMING_TEXT_FIELDS)
# Computed once at import, so concurrent Clients never see partial results.
_ENCODING_REACH_WITH_CODEC = _messages_reaching(
    _OUTGOING_TEXT_MESSAGES | {_VALUE_MESSAGE}
)
_ENCODING_REACH_WITHOUT_CODEC = _messages_reaching(
    _OUTGOING_TEXT_MESSAGES | {"dex.CloseDecision"}
)
_DECODING_REACH_WITH_VALUES = _messages_reaching(
    _INCOMING_TEXT_MESSAGES | {_VALUE_MESSAGE}
)
_DECODING_REACH_TEXT_ONLY = _messages_reaching(_INCOMING_TEXT_MESSAGES)
