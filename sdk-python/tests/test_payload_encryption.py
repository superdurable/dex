# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Edge cases of payload encryption that integration tests cannot reach."""

from __future__ import annotations

import asyncio
import json
import logging
from collections.abc import Sequence
from pathlib import Path
from typing import Any, cast
from unittest.mock import AsyncMock, patch

import grpc
import pytest
from google.protobuf import any_pb2
from google.protobuf.message import Message
from google.rpc import status_pb2

from dex import (
    AesGcmPayloadCodec,
    AsyncClient,
    BlobCacheConfig,
    Client,
    ClientOptions,
    Context,
    DexServiceError,
    FilePayloadEncryptionKeyProvider,
    Flow,
    PayloadCompression,
    PayloadContext,
    PayloadEncryptionError,
    PayloadEncryptionOptions,
    PayloadErrorCategory,
    Registry,
    RPCResult,
    StepList,
    ValueMappingError,
    WorkerInvocationError,
    open_blob_cache,
    retry_after,
    rpc,
)
from dex._grpc_errors import translate_rpc_error
from dex._payload_message_codec import (
    ENCODED_TEXT_PREFIX,
    PAYLOAD_ENVELOPE_ENCODING,
    PLAINTEXT_VALUE_FIELDS,
    AsyncPayloadMessageCodec,
    PayloadMessageCodec,
    PayloadScope,
)
from dex._pull_worker import PullStream, PullWorkerRuntime
from dex._value_hydrator import BlobHydrationError
from dex._value_mapper import ValueMapper
from dex.codec import CodecRegistry
from dex.dexpb import dex_pb2 as pb
from dex.worker_options import PullWorkerOptions

KEY = bytes(range(32))
SCOPE = PayloadScope(PayloadContext("OrderFlow", "order-1"), "Step-1")


class MemoryKeyProvider:
    def __init__(self, keys: dict[str, bytes], current_key_id: str) -> None:
        self.keys = keys
        self.current = current_key_id

    def current_key_id(self, context: PayloadContext) -> str:
        return self.current

    def key(self, context: PayloadContext, key_id: str) -> bytes:
        return self.keys[key_id]


class AsyncCodec:
    def __init__(self) -> None:
        self.inner = AesGcmPayloadCodec(MemoryKeyProvider({"k1": KEY}, "k1"))

    async def encode(
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> list[pb.PayloadEnvelope]:
        return self.inner.encode(context, plaintexts)

    async def decode(
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> list[bytes]:
        return self.inner.decode(context, envelopes)


def options(**changes: Any) -> PayloadEncryptionOptions:
    codec = AesGcmPayloadCodec(MemoryKeyProvider({"k1": KEY}, "k1"))
    return PayloadEncryptionOptions(codec, **changes)


OUTGOING_MESSAGE_TYPES: tuple[type[Message], ...] = (
    pb.PollWorkerTaskRequest,
    pb.StartFlowRequest,
    pb.InvokeRPCRequest,
    pb.WriteStreamRequest,
    pb.StopFlowRequest,
    pb.CloneForkFlowRequest,
    pb.WaitForAttributeRequest,
)
ValuePath = tuple[Any, ...]


def value_paths(descriptor: Any, prefix: ValuePath = ()) -> list[ValuePath]:
    """Return every field path from ``descriptor`` to a ``dex.Value``."""
    paths: list[ValuePath] = []
    visited = {field.message_type.full_name for field in prefix}
    for field in descriptor.fields:
        item_type = field.message_type
        if item_type is not None and item_type.GetOptions().map_entry:
            item_type = item_type.fields_by_name["value"].message_type
        if item_type is None:
            continue
        if item_type.full_name == "dex.Value":
            paths.append(prefix + (field,))
        elif field.message_type.full_name not in visited:
            paths.extend(value_paths(item_type, prefix + (field,)))
    return paths


def path_child(message: Any, field: Any) -> Any:
    container = getattr(message, field.name)
    if field.message_type.GetOptions().map_entry:
        return container["key"]
    if field.is_repeated:
        return container[0] if len(container) else container.add()
    return container


def path_value(message: Message, path: ValuePath) -> pb.Value:
    current: Any = message
    for field in path:
        current = path_child(current, field)
    assert isinstance(current, pb.Value)
    return current


def path_name(path: ValuePath) -> str:
    return ".".join(field.name for field in path)


OUTGOING_VALUE_PATHS = [
    (message_type, path)
    for message_type in OUTGOING_MESSAGE_TYPES
    for path in value_paths(message_type.DESCRIPTOR)
]


@pytest.mark.parametrize(
    "message_type, path",
    OUTGOING_VALUE_PATHS,
    ids=[f"{t.__name__}.{path_name(p)}" for t, p in OUTGOING_VALUE_PATHS],
)
def test_every_outgoing_value_path_is_encrypted_or_declared_plaintext(
    message_type: type[Message], path: ValuePath
) -> None:
    message = message_type()
    path_value(message, path).string_value = "secret"
    parent = path[-2].message_type.full_name if len(path) > 1 else ""
    parents = {field.containing_type.full_name for field in path}
    payloads = PayloadMessageCodec(options())
    if "dex.ActionPermissionMapping" in parents:
        # Unregistered Action condition Attributes are rejected, never sent.
        with pytest.raises(ValueError, match="plaintext"):
            payloads.encode_message(message, SCOPE)
        return
    value = path_value(payloads.encode_message(message, SCOPE), path)
    if (path[-1].containing_type.full_name, path[-1].name) in PLAINTEXT_VALUE_FIELDS:
        assert value.string_value == "secret"
    else:
        assert value.obj_value.encoding == PAYLOAD_ENVELOPE_ENCODING, parent
        assert b"secret" not in value.obj_value.payload


def test_outgoing_value_paths_cover_known_worker_outputs() -> None:
    names = {path_name(path) for _, path in OUTGOING_VALUE_PATHS}
    assert "task_frame.execute.heartbeat.value" in names
    assert "task_frame.wait_for.stream_write.value" in names
    assert "task_frame.execute.result.step_decision.close_decision.close_input" in names
    assert len(OUTGOING_VALUE_PATHS) > 30


def test_outgoing_text_fields_use_the_prefix_and_decode_back() -> None:
    payloads = PayloadMessageCodec(options())
    error = pb.WorkerErrorResponse(detail="card declined", stack_trace="trace")
    encoded = payloads.encode_message(error, SCOPE)
    assert encoded.detail.startswith(ENCODED_TEXT_PREFIX)
    assert encoded.stack_trace.startswith(ENCODED_TEXT_PREFIX)
    decoded = payloads.decode_message(encoded, SCOPE)
    assert (decoded.detail, decoded.stack_trace) == ("card declined", "trace")
    assert error.detail == "card declined"
    empty = payloads.encode_message(pb.WorkerErrorResponse(error_type="E"), SCOPE)
    assert (empty.detail, empty.error_type) == ("", "E")


def test_error_text_option_off_still_encrypts_prefix_collisions() -> None:
    payloads = PayloadMessageCodec(options(encrypt_error_text=False))
    plain = payloads.encode_message(pb.StopFlowRequest(reason="done"), SCOPE)
    colliding = payloads.encode_message(
        pb.StopFlowRequest(reason=ENCODED_TEXT_PREFIX + "x"), SCOPE
    )
    assert plain.reason == "done"
    assert payloads.decode_text(colliding.reason, SCOPE.context) == (
        ENCODED_TEXT_PREFIX + "x"
    )


def test_writer_without_codec_escapes_prefix_and_reader_fails_closed() -> None:
    payloads = PayloadMessageCodec(None)
    escaped = payloads.encode_message(
        pb.StopFlowRequest(reason=ENCODED_TEXT_PREFIX + "x"), SCOPE
    )
    assert escaped.reason == " " + ENCODED_TEXT_PREFIX + "x"
    with pytest.raises(ValueMappingError, match="configure a PayloadCodec"):
        payloads.decode_text(ENCODED_TEXT_PREFIX + "AAAA", SCOPE.context)


def test_marked_value_without_codec_raises_the_specified_message() -> None:
    sender = PayloadMessageCodec(options())
    encrypted = sender.encode_message(
        pb.WriteStreamRequest(value=pb.Value(string_value="secret")), SCOPE
    )
    codecless = PayloadMessageCodec(None)
    with pytest.raises(ValueMappingError) as raised:
        codecless.decode_values([encrypted.value], SCOPE.context)
    assert "value is encrypted; configure a PayloadCodec" in str(raised.value)
    assert "secret" not in str(raised.value)
    # A Worker request fails when it is opened, even for an unread Attribute.
    request = pb.InvokeExecuteMethodRequest(flow_type="OrderFlow")
    request.attributes.add(key="unread", value=encrypted.value)
    with pytest.raises(ValueMappingError, match="configure a PayloadCodec"):
        codecless.decode_message(request, SCOPE)
    values = ValueMapper(CodecRegistry())
    with pytest.raises(ValueMappingError, match="configure a PayloadCodec"):
        values.decode(encrypted.value, values.codec(str))
    plain = pb.Value(string_value="plain")
    assert codecless.decode_values([plain], SCOPE.context)[0] is plain


def test_key_provider_failures_never_leak_provider_text(
    caplog: pytest.LogCaptureFixture,
) -> None:
    class LeakingKeyProvider:
        def current_key_id(self, context: PayloadContext) -> str:
            raise Exception("KEY_MATERIAL_SENTINEL")

        def key(self, context: PayloadContext, key_id: str) -> bytes:
            raise Exception("KEY_MATERIAL_SENTINEL")

    leaking = PayloadEncryptionOptions(AesGcmPayloadCodec(LeakingKeyProvider()))
    working = PayloadMessageCodec(options())
    encrypted = working.encode_message(
        pb.WriteStreamRequest(value=pb.Value(string_value="secret")), SCOPE
    ).value
    payloads = PayloadMessageCodec(leaking)
    failures: list[PayloadEncryptionError] = []
    for action in (
        lambda: payloads.encode_message(
            pb.WriteStreamRequest(value=pb.Value(string_value="secret")), SCOPE
        ),
        lambda: payloads.decode_values([encrypted], SCOPE.context),
    ):
        with pytest.raises(PayloadEncryptionError) as raised:
            action()
        failures.append(raised.value)
    for failure in failures:
        assert failure.category is PayloadErrorCategory.UNKNOWN_KEY
        for text in (str(failure), repr(failure)):
            assert "SENTINEL" not in text
        assert failure.__cause__ is None and failure.__context__ is None
    with caplog.at_level(logging.DEBUG):
        sent = asyncio.run(
            _sent_worker_error(_raised(RuntimeError("card 4242")), leaking)
        )
    assert "SENTINEL" not in str(sent) and "SENTINEL" not in caplog.text
    assert sent.detail.endswith("unknown_key")


SENTINEL = "KEY_MATERIAL_SENTINEL"


def _sentinel_errors() -> list[Exception]:
    reported = PayloadEncryptionError(
        "decode", PayloadErrorCategory.UNKNOWN_KEY, SENTINEL, "k1"
    )
    forged = PayloadEncryptionError("decode", PayloadErrorCategory.MALFORMED, "x")
    forged.key_id = SENTINEL
    forged.category = SENTINEL  # type: ignore[assignment]
    return [RuntimeError(SENTINEL), reported, forged]


class SentinelCodec:
    def __init__(self, error: BaseException) -> None:
        self.error = error

    def encode(
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> list[pb.PayloadEnvelope]:
        raise self.error

    def decode(
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> list[bytes]:
        raise self.error


class AsyncSentinelCodec(SentinelCodec):
    async def encode(  # type: ignore[override]
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> list[pb.PayloadEnvelope]:
        raise self.error

    async def decode(  # type: ignore[override]
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> list[bytes]:
        raise self.error


def _assert_without_sentinel(failure: PayloadEncryptionError) -> None:
    for text in (str(failure), repr(failure), str(failure.key_id)):
        assert SENTINEL not in text
    assert isinstance(failure.category, PayloadErrorCategory)
    assert failure.__cause__ is None and failure.__context__ is None


@pytest.mark.parametrize("error", _sentinel_errors(), ids=["plain", "own", "forged"])
def test_custom_codec_exceptions_never_cross_the_sdk_boundary(
    error: Exception,
) -> None:
    encrypted = (
        PayloadMessageCodec(options())
        .encode_message(
            pb.WriteStreamRequest(value=pb.Value(string_value="secret")), SCOPE
        )
        .value
    )
    message = pb.WriteStreamRequest(value=pb.Value(string_value="secret"))
    failures: list[PayloadEncryptionError] = []
    sync_payloads = PayloadMessageCodec(PayloadEncryptionOptions(SentinelCodec(error)))
    for action in (
        lambda: sync_payloads.encode_message(message, SCOPE),
        lambda: sync_payloads.decode_values([encrypted], SCOPE.context),
    ):
        with pytest.raises(PayloadEncryptionError) as raised:
            action()
        failures.append(raised.value)

    async def run_async(codec: Any) -> None:
        payloads = AsyncPayloadMessageCodec(PayloadEncryptionOptions(codec))
        for operation in ("encode", "decode"):
            with pytest.raises(PayloadEncryptionError) as raised:
                if operation == "encode":
                    await payloads.encode_message(message, SCOPE)
                else:
                    await payloads.decode_values([encrypted], SCOPE.context)
            failures.append(raised.value)

    asyncio.run(run_async(SentinelCodec(error)))
    asyncio.run(run_async(AsyncSentinelCodec(error)))
    assert len(failures) == 6
    for failure in failures:
        _assert_without_sentinel(failure)
    decode_failure = failures[1]
    if error is _sentinel_errors_own_marker(error):
        assert decode_failure.category is PayloadErrorCategory.UNKNOWN_KEY
        assert decode_failure.key_id == "k1"
    else:
        assert decode_failure.category is PayloadErrorCategory.CODEC_FAILED
        assert decode_failure.key_id == ""


def _sentinel_errors_own_marker(error: Exception) -> Exception | None:
    """Return ``error`` when it is the codec's own well-formed error."""
    if (
        isinstance(error, PayloadEncryptionError)
        and error.category is PayloadErrorCategory.UNKNOWN_KEY
    ):
        return error
    return None


class MutatingDelegateCodec:
    """Delegate to the built-in codec, then forge its errors before re-raising."""

    def __init__(self) -> None:
        self.inner = AesGcmPayloadCodec(MemoryKeyProvider({"k2": KEY}, "k9"))

    def _forge(self, error: PayloadEncryptionError) -> PayloadEncryptionError:
        error.key_id = SENTINEL
        error.category = SENTINEL  # type: ignore[assignment]
        error.args = (SENTINEL,)
        error.detail = SENTINEL
        return error

    def encode(
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> list[pb.PayloadEnvelope]:
        try:
            return self.inner.encode(context, plaintexts)
        except PayloadEncryptionError as error:
            raise self._forge(error)

    def decode(
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> list[bytes]:
        try:
            return self.inner.decode(context, envelopes)
        except PayloadEncryptionError as error:
            raise self._forge(error)


class AsyncMutatingDelegateCodec(MutatingDelegateCodec):
    async def encode(  # type: ignore[override]
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> list[pb.PayloadEnvelope]:
        return super().encode(context, plaintexts)

    async def decode(  # type: ignore[override]
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> list[bytes]:
        return super().decode(context, envelopes)


def test_forged_built_in_codec_errors_are_rebuilt_at_the_boundary(
    caplog: pytest.LogCaptureFixture,
) -> None:
    encrypted = (
        PayloadMessageCodec(options())
        .encode_message(
            pb.WriteStreamRequest(value=pb.Value(string_value="secret")), SCOPE
        )
        .value
    )
    message = pb.WriteStreamRequest(value=pb.Value(string_value="secret"))
    failures: list[PayloadEncryptionError] = []
    sync_payloads = PayloadMessageCodec(
        PayloadEncryptionOptions(MutatingDelegateCodec())
    )
    for action in (
        lambda: sync_payloads.encode_message(message, SCOPE),
        lambda: sync_payloads.decode_values([encrypted], SCOPE.context),
    ):
        with pytest.raises(PayloadEncryptionError) as raised:
            action()
        failures.append(raised.value)

    async def run_async() -> None:
        payloads = AsyncPayloadMessageCodec(
            PayloadEncryptionOptions(AsyncMutatingDelegateCodec())
        )
        with pytest.raises(PayloadEncryptionError) as encode_failure:
            await payloads.encode_message(message, SCOPE)
        with pytest.raises(PayloadEncryptionError) as decode_failure:
            await payloads.decode_values([encrypted], SCOPE.context)
        failures.extend([encode_failure.value, decode_failure.value])

    asyncio.run(run_async())
    for failure in failures:
        _assert_without_sentinel(failure)
        assert failure.category is PayloadErrorCategory.CODEC_FAILED
        assert failure.key_id == ""
    with caplog.at_level(logging.DEBUG):
        sent = asyncio.run(
            _executed_worker_frames(
                PayloadEncryptionOptions(
                    AsyncMutatingDelegateCodec(), encrypt_error_text=False
                )
            )
        )
    assert SENTINEL not in "".join(str(request) for request in sent)
    assert SENTINEL not in caplog.text


class HostilePayloadError(PayloadEncryptionError):
    """Every attribute read and conversion raises an exception holding the sentinel."""

    def __init__(self) -> None:
        Exception.__init__(self)

    @property
    def category(self) -> PayloadErrorCategory:  # type: ignore[override]
        raise RuntimeError(SENTINEL)

    @property
    def key_id(self) -> str:  # type: ignore[override]
        raise RuntimeError(SENTINEL)

    def __str__(self) -> str:
        raise RuntimeError(SENTINEL)

    def __repr__(self) -> str:
        raise RuntimeError(SENTINEL)


class HostileDispatcher:
    async def invoke_rpc(
        self, request: pb.InvokeWorkerRPCRequest, is_active: Any
    ) -> pb.InvokeWorkerRPCResponse:
        raise HostilePayloadError()


def test_hostile_codec_error_attributes_fall_back_to_codec_failed(
    caplog: pytest.LogCaptureFixture,
) -> None:
    payloads = PayloadMessageCodec(
        PayloadEncryptionOptions(SentinelCodec(HostilePayloadError()))
    )
    with pytest.raises(PayloadEncryptionError) as raised:
        payloads.encode_message(
            pb.WriteStreamRequest(value=pb.Value(string_value="secret")), SCOPE
        )
    _assert_without_sentinel(raised.value)
    assert raised.value.category is PayloadErrorCategory.CODEC_FAILED
    assert raised.value.key_id == ""
    hostile_codec = PayloadEncryptionOptions(
        SentinelCodec(HostilePayloadError()), encrypt_error_text=False
    )
    with caplog.at_level(logging.DEBUG):
        sent = asyncio.run(_executed_worker_frames(hostile_codec))
    assert SENTINEL not in "".join(str(request) for request in sent)
    assert SENTINEL not in caplog.text


def test_handler_payload_error_is_reported_from_fixed_fields_only(
    caplog: pytest.LogCaptureFixture,
) -> None:
    async def execute() -> list[Any]:
        runtime = PullWorkerRuntime(
            Registry([]),
            HostileDispatcher(),
            PullWorkerOptions(max_concurrent_tasks=1, app_worker_id="hostile-worker"),
            "localhost:8801",
            None,
            frame_payloads=AsyncPayloadMessageCodec(options(encrypt_error_text=False)),
        )
        stream = PullStream(runtime, None, pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC)
        task = pb.PollWorkerTask(
            task_id="task",
            rpc=pb.InvokeWorkerRPCRequest(
                flow_type="OrderFlow", context=pb.Context(flow_id="order-1")
            ),
        )
        with patch.object(stream, "_send", new_callable=AsyncMock) as send_mock:
            await stream._execute(task)
        return [call.args[0] for call in send_mock.await_args_list]

    with caplog.at_level(logging.DEBUG):
        sent = asyncio.run(execute())
    worker_error = sent[-1].task_frame.worker_error
    assert worker_error.detail == (
        "payload codec_failed: Dex could not encrypt or decrypt a payload"
    )
    assert worker_error.error_type == "dex.payload_encryption.PayloadEncryptionError"
    assert worker_error.stack_trace == ""
    assert SENTINEL not in "".join(str(request) for request in sent)
    assert SENTINEL not in caplog.text


class EnvelopeMutatingCodec:
    """Rewrite the envelopes it receives to a sentinel key ID, then misbehave."""

    def __init__(self, behavior: str) -> None:
        self.behavior = behavior

    def encode(
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> list[pb.PayloadEnvelope]:
        raise NotImplementedError

    def decode(
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> list[bytes]:
        for envelope in envelopes:
            envelope.key_id = SENTINEL
        if self.behavior == "error":
            raise PayloadEncryptionError(
                "decode", PayloadErrorCategory.UNKNOWN_KEY, "x", SENTINEL
            )
        if self.behavior == "invalid":
            return [b"\xff\xff\xff" for _ in envelopes]
        return [pb.Value(string_value="a" * 4096).SerializeToString()] * len(envelopes)


class AsyncEnvelopeMutatingCodec(EnvelopeMutatingCodec):
    async def encode(  # type: ignore[override]
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> list[pb.PayloadEnvelope]:
        raise NotImplementedError

    async def decode(  # type: ignore[override]
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> list[bytes]:
        return super().decode(context, envelopes)


class DecodingDispatcher:
    def __init__(self, payloads: AsyncPayloadMessageCodec, value: pb.Value) -> None:
        self.payloads = payloads
        self.value = value

    async def invoke_rpc(
        self, request: pb.InvokeWorkerRPCRequest, is_active: Any
    ) -> pb.InvokeWorkerRPCResponse:
        await self.payloads.decode_values([self.value], SCOPE.context)
        return pb.InvokeWorkerRPCResponse()


@pytest.mark.parametrize("behavior", ["error", "invalid", "oversize"])
def test_codec_cannot_plant_a_key_id_through_the_envelopes_it_receives(
    behavior: str, caplog: pytest.LogCaptureFixture
) -> None:
    encrypted = (
        PayloadMessageCodec(options())
        .encode_message(
            pb.WriteStreamRequest(value=pb.Value(string_value="secret")), SCOPE
        )
        .value
    )
    expected = {
        "error": PayloadErrorCategory.UNKNOWN_KEY,
        "invalid": PayloadErrorCategory.INVALID_PLAINTEXT,
        "oversize": PayloadErrorCategory.LIMIT_EXCEEDED,
    }[behavior]
    failures: list[PayloadEncryptionError] = []
    sync_payloads = PayloadMessageCodec(
        PayloadEncryptionOptions(
            EnvelopeMutatingCodec(behavior), max_plaintext_bytes=1024
        )
    )
    with pytest.raises(PayloadEncryptionError) as raised:
        sync_payloads.decode_values([encrypted], SCOPE.context)
    failures.append(raised.value)
    async_options = PayloadEncryptionOptions(
        AsyncEnvelopeMutatingCodec(behavior),
        max_plaintext_bytes=1024,
        encrypt_error_text=False,
    )

    async def decode_async() -> None:
        with pytest.raises(PayloadEncryptionError) as async_raised:
            await AsyncPayloadMessageCodec(async_options).decode_values(
                [encrypted], SCOPE.context
            )
        failures.append(async_raised.value)

    asyncio.run(decode_async())
    for failure in failures:
        _assert_without_sentinel(failure)
        assert failure.category is expected
        assert failure.key_id in ("", "k1")

    async def execute() -> list[Any]:
        payloads = AsyncPayloadMessageCodec(async_options)
        runtime = PullWorkerRuntime(
            Registry([]),
            DecodingDispatcher(payloads, encrypted),
            PullWorkerOptions(max_concurrent_tasks=1, app_worker_id="mutating-worker"),
            "localhost:8801",
            None,
            frame_payloads=payloads,
        )
        stream = PullStream(runtime, None, pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC)
        task = pb.PollWorkerTask(
            task_id="task",
            rpc=pb.InvokeWorkerRPCRequest(
                flow_type="OrderFlow", context=pb.Context(flow_id="order-1")
            ),
        )
        with patch.object(stream, "_send", new_callable=AsyncMock) as send_mock:
            await stream._execute(task)
        return [call.args[0] for call in send_mock.await_args_list]

    with caplog.at_level(logging.DEBUG):
        sent = asyncio.run(execute())
    assert sent[-1].task_frame.worker_error.detail.startswith(
        f"payload {expected.value}:"
    )
    assert SENTINEL not in "".join(str(request) for request in sent)
    assert SENTINEL not in caplog.text


class RecordingDispatcher:
    async def invoke_rpc(
        self, request: pb.InvokeWorkerRPCRequest, is_active: Any
    ) -> pb.InvokeWorkerRPCResponse:
        return pb.InvokeWorkerRPCResponse(output=pb.Value(string_value="result"))


async def _executed_worker_frames(encryption: PayloadEncryptionOptions) -> list[Any]:
    """Run one RPC task whose result frame encryption fails; return the sent requests."""
    runtime = PullWorkerRuntime(
        Registry([]),
        RecordingDispatcher(),
        PullWorkerOptions(max_concurrent_tasks=1, app_worker_id="sentinel-worker"),
        "localhost:8801",
        None,
        frame_payloads=AsyncPayloadMessageCodec(encryption),
    )
    stream = PullStream(runtime, None, pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC)
    task = pb.PollWorkerTask(
        task_id="task",
        rpc=pb.InvokeWorkerRPCRequest(
            flow_type="OrderFlow", context=pb.Context(flow_id="order-1")
        ),
    )
    with patch.object(stream, "_send", new_callable=AsyncMock) as send_mock:
        await stream._execute(task)
    sent = [call.args[0] for call in send_mock.await_args_list]
    assert sent and sent[-1].task_frame.HasField("worker_error")
    return sent


def test_worker_never_logs_or_sends_custom_codec_exception_text(
    caplog: pytest.LogCaptureFixture,
) -> None:
    encryption = PayloadEncryptionOptions(
        SentinelCodec(RuntimeError(SENTINEL)), encrypt_error_text=False
    )
    with caplog.at_level(logging.DEBUG):
        sent = asyncio.run(_executed_worker_frames(encryption))
    assert sent and sent[-1].task_frame.HasField("worker_error")
    assert SENTINEL not in "".join(str(request) for request in sent)
    assert SENTINEL not in caplog.text
    assert "codec_failed" in sent[-1].task_frame.worker_error.detail


def test_custom_codec_receives_the_plaintext_limit_when_it_declares_it() -> None:
    received: list[int] = []
    inner = AesGcmPayloadCodec(MemoryKeyProvider({"k1": KEY}, "k1"))

    class LimitAwareCodec:
        def encode(
            self, context: PayloadContext, plaintexts: Sequence[bytes]
        ) -> list[pb.PayloadEnvelope]:
            return inner.encode(context, plaintexts)

        def decode(
            self,
            context: PayloadContext,
            envelopes: Sequence[pb.PayloadEnvelope],
            *,
            max_plaintext_bytes: int | None = None,
        ) -> list[bytes]:
            assert max_plaintext_bytes is not None
            received.append(max_plaintext_bytes)
            return inner.decode(context, envelopes)

    class OversizedCodec(LimitAwareCodec):
        def decode(  # type: ignore[override]
            self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
        ) -> list[bytes]:
            return [pb.Value(string_value="a" * 2048).SerializeToString()]

    payloads = PayloadMessageCodec(
        PayloadEncryptionOptions(LimitAwareCodec(), max_plaintext_bytes=1024)
    )
    encrypted = payloads.encode_message(
        pb.WriteStreamRequest(value=pb.Value(string_value="small")), SCOPE
    ).value
    assert payloads.decode_values([encrypted], SCOPE.context)[0].string_value == (
        "small"
    )

    async def decode_async() -> str:
        async_payloads = AsyncPayloadMessageCodec(
            PayloadEncryptionOptions(LimitAwareCodec(), max_plaintext_bytes=1024)
        )
        decoded = await async_payloads.decode_values([encrypted], SCOPE.context)
        return decoded[0].string_value

    assert asyncio.run(decode_async()) == "small"
    assert received == [1024, 1024]
    # A codec without the keyword still gets the SDK's check after it returns.
    oversized = PayloadMessageCodec(
        PayloadEncryptionOptions(OversizedCodec(), max_plaintext_bytes=1024)
    )
    with pytest.raises(PayloadEncryptionError) as raised:
        oversized.decode_values([encrypted], SCOPE.context)
    assert raised.value.category is PayloadErrorCategory.LIMIT_EXCEEDED


def test_built_in_codec_never_decompresses_past_the_outer_limit() -> None:
    codec = AesGcmPayloadCodec(
        MemoryKeyProvider({"k1": KEY}, "k1"),
        compression=PayloadCompression.GZIP,
        max_decompressed_bytes=1 << 20,
    )
    sender = PayloadMessageCodec(PayloadEncryptionOptions(codec))
    encrypted = sender.encode_message(
        pb.WriteStreamRequest(value=pb.Value(string_value="a" * 4096)), SCOPE
    ).value
    assert len(encrypted.obj_value.payload) < 1024
    reader = PayloadMessageCodec(
        PayloadEncryptionOptions(codec, max_plaintext_bytes=1024)
    )
    with pytest.raises(PayloadEncryptionError) as raised:
        reader.decode_values([encrypted], SCOPE.context)
    assert raised.value.category is PayloadErrorCategory.LIMIT_EXCEEDED

    async def decode_async() -> None:
        async_reader = AsyncPayloadMessageCodec(
            PayloadEncryptionOptions(codec, max_plaintext_bytes=1024)
        )
        await async_reader.decode_values([encrypted], SCOPE.context)

    with pytest.raises(PayloadEncryptionError, match="limit_exceeded"):
        asyncio.run(decode_async())


def test_kind_switches_leave_strings_and_primitives_in_plaintext() -> None:
    payloads = PayloadMessageCodec(
        options(encrypt_string_values=False, encrypt_primitive_values=False)
    )
    request = pb.StepDecision()
    for value in (
        pb.Value(string_value="s"),
        pb.Value(int_value=1),
        pb.Value(bool_value=True),
        pb.Value(obj_value=pb.EncodedObject(encoding="json", payload=b"{}")),
    ):
        request.next_steps.add(step_input=value)
    encoded = payloads.encode_message(request, SCOPE)
    kinds = [step.step_input.WhichOneof("kind") for step in encoded.next_steps]
    assert kinds == ["string_value", "int_value", "bool_value", "obj_value"]
    assert encoded.next_steps[3].step_input.obj_value.encoding == "dex/1"


def test_application_object_with_reserved_encoding_is_rejected() -> None:
    message = pb.WriteStreamRequest(
        value=pb.Value(obj_value=pb.EncodedObject(encoding="dex/9", payload=b""))
    )
    with pytest.raises(PayloadEncryptionError) as raised:
        PayloadMessageCodec(options()).encode_message(message, SCOPE)
    assert raised.value.category is PayloadErrorCategory.INVALID_PLAINTEXT


def test_sub_flow_condition_uses_the_child_flow_context() -> None:
    contexts: list[PayloadContext] = []

    class RecordingCodec:
        inner = AesGcmPayloadCodec(MemoryKeyProvider({"k1": KEY}, "k1"))

        def encode(
            self, context: PayloadContext, plaintexts: Sequence[bytes]
        ) -> list[pb.PayloadEnvelope]:
            contexts.append(context)
            return self.inner.encode(context, plaintexts)

        def decode(
            self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
        ) -> list[bytes]:
            return self.inner.decode(context, envelopes)

    response = pb.InvokeWaitForMethodResponse()
    condition = response.waiting_condition.sub_flow_conditions.add(
        sub_flow_type="ChildFlow", sub_flow_index=2
    )
    condition.step_input.string_value = "child input"
    payloads = PayloadMessageCodec(PayloadEncryptionOptions(RecordingCodec()))
    payloads.encode_message(response, SCOPE)
    assert contexts == [PayloadContext("ChildFlow", "SubFlow:order-1-Step-1-2")]


def test_async_driver_accepts_sync_and_async_codecs() -> None:
    async def round_trip(codec: Any) -> str:
        payloads = AsyncPayloadMessageCodec(PayloadEncryptionOptions(codec))
        message = pb.WriteStreamRequest(value=pb.Value(string_value="hello"))
        encoded = await payloads.encode_message(message, SCOPE)
        assert encoded.value.obj_value.encoding == "dex/1"
        decoded = await payloads.decode_values([encoded.value], SCOPE.context)
        return decoded[0].string_value

    sync_codec = AesGcmPayloadCodec(MemoryKeyProvider({"k1": KEY}, "k1"))
    assert asyncio.run(round_trip(sync_codec)) == "hello"
    assert asyncio.run(round_trip(AsyncCodec())) == "hello"


def test_sync_client_rejects_an_async_codec(tmp_path: Path) -> None:
    cache = open_blob_cache(BlobCacheConfig(str(tmp_path / "cache"), 1024 * 1024))
    try:
        encryption = PayloadEncryptionOptions(AsyncCodec())
        with pytest.raises(TypeError, match="synchronous PayloadCodec"):
            Client(Registry([]), cache, ClientOptions(payload_encryption=encryption))
    finally:
        cache.close()


async def _sent_worker_error(
    failure: BaseException, encryption: PayloadEncryptionOptions
) -> pb.WorkerErrorResponse:
    runtime = PullWorkerRuntime(
        Registry([]),
        None,
        PullWorkerOptions(max_concurrent_tasks=1, app_worker_id="encrypting-worker"),
        "localhost:8801",
        None,
        frame_payloads=AsyncPayloadMessageCodec(encryption),
    )
    stream = PullStream(runtime, None, pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC)
    with patch.object(stream, "_send", new_callable=AsyncMock) as send_mock:
        await stream._send_error("task", failure, None, SCOPE)
    assert send_mock.await_args is not None
    return cast(
        pb.WorkerErrorResponse, send_mock.await_args.args[0].task_frame.worker_error
    )


def _raised(error: BaseException) -> BaseException:
    try:
        raise error
    except BaseException as caught:
        return caught


def test_worker_error_text_is_encrypted_except_sdk_blob_errors() -> None:
    user_error = asyncio.run(
        _sent_worker_error(_raised(RuntimeError("card 4242")), options())
    )
    assert user_error.detail.startswith(ENCODED_TEXT_PREFIX)
    assert user_error.stack_trace.startswith(ENCODED_TEXT_PREFIX)
    assert user_error.error_type.endswith("RuntimeError")
    blob_error = asyncio.run(
        _sent_worker_error(
            _raised(BlobHydrationError("LoadBlobs omitted blob b1")), options()
        )
    )
    assert blob_error.detail == "LoadBlobs omitted blob b1"
    assert blob_error.stack_trace.startswith(ENCODED_TEXT_PREFIX)


def test_worker_error_falls_back_to_fixed_text_when_encryption_fails() -> None:
    class FailingCodec:
        def encode(
            self, context: PayloadContext, plaintexts: Sequence[bytes]
        ) -> list[pb.PayloadEnvelope]:
            raise ConnectionError("kms unavailable: card 4242")

        def decode(
            self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
        ) -> list[bytes]:
            raise ConnectionError("kms unavailable")

    sent = asyncio.run(
        _sent_worker_error(
            RuntimeError("card 4242"), PayloadEncryptionOptions(FailingCodec())
        )
    )
    assert sent.detail == (
        "Dex Python SDK could not encrypt the Worker error text: codec_failed"
    )
    assert sent.stack_trace == ""


def test_gzip_round_trip_and_key_rotation() -> None:
    provider = MemoryKeyProvider({"k1": KEY, "k2": bytes(32)}, "k1")
    codec = AesGcmPayloadCodec(provider, compression=PayloadCompression.GZIP)
    plaintext = pb.Value(string_value="repeat " * 100).SerializeToString()
    old = codec.encode(PayloadContext("", "f"), [plaintext])[0]
    provider.current = "k2"
    new = codec.encode(PayloadContext("", "f"), [plaintext])[0]
    assert (old.key_id, new.key_id) == ("k1", "k2")
    assert old.compression == pb.PAYLOAD_COMPRESSION_GZIP
    assert len(old.data) < len(plaintext)
    assert codec.decode(PayloadContext("", "f"), [old, new]) == [plaintext] * 2
    assert old.data[:12] != new.data[:12]


@pytest.mark.parametrize(
    "document",
    [
        {
            "current_key_id": "k9",
            "keys": {"k1": "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="},
        },
        {"current_key_id": "k1", "keys": {"k1": "AAEC"}},
        {
            "current_key_id": "k 1",
            "keys": {"k 1": "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="},
        },
        {
            "current_key_id": "k1",
            "keys": {"k1": "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"},
        },
    ],
)
def test_key_file_rejects_invalid_documents_without_key_bytes(
    tmp_path: Path, document: dict[str, Any]
) -> None:
    path = tmp_path / "keys.json"
    path.write_text(json.dumps(document))
    path.chmod(0o600)
    with pytest.raises(ValueError) as raised:
        FilePayloadEncryptionKeyProvider(path)
    assert "AAEC" not in str(raised.value)


def test_key_file_readable_by_others_logs_a_warning(
    tmp_path: Path, caplog: pytest.LogCaptureFixture
) -> None:
    path = tmp_path / "keys.json"
    path.write_text(
        json.dumps(
            {
                "current_key_id": "k1",
                "keys": {"k1": "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="},
            }
        )
    )
    path.chmod(0o644)
    with caplog.at_level(logging.WARNING):
        provider = FilePayloadEncryptionKeyProvider(path)
    assert "accessible by other users" in caplog.text
    assert provider.key(PayloadContext("", "f"), "k1") == KEY
    assert "AAEC" not in repr(provider)


class HostileSequence(Sequence[Any]):
    """A codec result whose length and iteration run application code."""

    def __len__(self) -> int:
        raise RuntimeError(SENTINEL)

    def __getitem__(self, index: Any) -> Any:
        raise RuntimeError(SENTINEL)

    def __iter__(self) -> Any:
        raise RuntimeError(SENTINEL)


class HostileResultCodec:
    def encode(self, context: PayloadContext, plaintexts: Sequence[bytes]) -> Any:
        return HostileSequence()

    def decode(
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> Any:
        return HostileSequence()


class AsyncHostileResultCodec:
    async def encode(self, context: PayloadContext, plaintexts: Sequence[bytes]) -> Any:
        return HostileSequence()

    async def decode(
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> Any:
        return HostileSequence()


def _encrypted_secret() -> pb.Value:
    return (
        PayloadMessageCodec(options())
        .encode_message(
            pb.WriteStreamRequest(value=pb.Value(string_value="secret")), SCOPE
        )
        .value
    )


def _boundary_failures(sync_codec: Any, async_codec: Any) -> list[BaseException]:
    """Run sync and async encode and decode; return what each raised."""
    encrypted = _encrypted_secret()
    message = pb.WriteStreamRequest(value=pb.Value(string_value="secret"))
    failures: list[BaseException] = []
    sync_payloads = PayloadMessageCodec(PayloadEncryptionOptions(sync_codec))
    for action in (
        lambda: sync_payloads.encode_message(message, SCOPE),
        lambda: sync_payloads.decode_values([encrypted], SCOPE.context),
    ):
        try:
            action()
        except BaseException as error:
            failures.append(error)

    async def run_async() -> None:
        payloads = AsyncPayloadMessageCodec(PayloadEncryptionOptions(async_codec))
        for operation in ("encode", "decode"):
            try:
                if operation == "encode":
                    await payloads.encode_message(message, SCOPE)
                else:
                    await payloads.decode_values([encrypted], SCOPE.context)
            except BaseException as error:
                failures.append(error)

    asyncio.run(run_async())
    return failures


def test_codec_result_objects_are_copied_inside_the_boundary(
    caplog: pytest.LogCaptureFixture,
) -> None:
    failures = _boundary_failures(HostileResultCodec(), AsyncHostileResultCodec())
    assert len(failures) == 4
    for failure in failures:
        assert isinstance(failure, PayloadEncryptionError)
        _assert_without_sentinel(failure)
        assert failure.category is PayloadErrorCategory.CODEC_FAILED
    plaintext_errors = PayloadEncryptionOptions(
        AsyncHostileResultCodec(), encrypt_error_text=False
    )
    with caplog.at_level(logging.DEBUG):
        sent = asyncio.run(_executed_worker_frames(plaintext_errors))
    assert SENTINEL not in "".join(str(request) for request in sent)
    assert SENTINEL not in caplog.text


class InterruptingCodec(SentinelCodec):
    def __init__(self) -> None:
        super().__init__(KeyboardInterrupt(SENTINEL))


class AsyncInterruptingCodec(AsyncSentinelCodec):
    def __init__(self) -> None:
        super().__init__(KeyboardInterrupt(SENTINEL))


class InterruptingPropertyError(PayloadEncryptionError):
    def __init__(self) -> None:
        Exception.__init__(self)

    @property
    def category(self) -> PayloadErrorCategory:  # type: ignore[override]
        raise KeyboardInterrupt(SENTINEL)

    @property
    def key_id(self) -> str:  # type: ignore[override]
        raise KeyboardInterrupt(SENTINEL)


class RaisingIterationResult(Sequence[Any]):
    """A codec result that raises the given exception while it is iterated."""

    def __init__(self, error: BaseException) -> None:
        self.error = error

    def __len__(self) -> int:
        raise self.error

    def __getitem__(self, index: Any) -> Any:
        raise self.error

    def __iter__(self) -> Any:
        raise self.error


class RaisingResultCodec:
    def __init__(self, error: BaseException) -> None:
        self.error = error

    def encode(self, context: PayloadContext, plaintexts: Sequence[bytes]) -> Any:
        return RaisingIterationResult(self.error)

    def decode(
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> Any:
        return RaisingIterationResult(self.error)


class AsyncRaisingResultCodec(RaisingResultCodec):
    async def encode(self, context: PayloadContext, plaintexts: Sequence[bytes]) -> Any:
        return RaisingIterationResult(self.error)

    async def decode(
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> Any:
        return RaisingIterationResult(self.error)


CONTROL_FLOW_TYPES: list[type[BaseException]] = [
    KeyboardInterrupt,
    SystemExit,
    GeneratorExit,
]


@pytest.mark.parametrize("control_flow_type", CONTROL_FLOW_TYPES)
@pytest.mark.parametrize("where", ["raised", "while-iterating-result"])
def test_control_flow_exceptions_are_rebuilt_fresh_without_text(
    control_flow_type: type[BaseException], where: str
) -> None:
    def codecs() -> tuple[Any, Any]:
        if where == "raised":
            return (
                SentinelCodec(control_flow_type(SENTINEL)),
                AsyncSentinelCodec(control_flow_type(SENTINEL)),
            )
        return (
            RaisingResultCodec(control_flow_type(SENTINEL)),
            AsyncRaisingResultCodec(control_flow_type(SENTINEL)),
        )

    failures = _boundary_failures(*codecs())
    assert len(failures) == 4
    for failure in failures:
        assert type(failure) is control_flow_type
        assert failure.args == ()
        assert failure.__cause__ is None and failure.__context__ is None


def test_sync_codec_returning_a_coroutine_fails_and_closes_it() -> None:
    created: list[Any] = []

    async def late_plaintexts() -> list[bytes]:
        return []

    class CoroutineReturningCodec(SentinelCodec):
        def decode(
            self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
        ) -> Any:
            coroutine = late_plaintexts()
            created.append(coroutine)
            return coroutine

    payloads = PayloadMessageCodec(
        PayloadEncryptionOptions(CoroutineReturningCodec(RuntimeError(SENTINEL)))
    )
    with pytest.raises(PayloadEncryptionError) as raised:
        payloads.decode_values([_encrypted_secret()], SCOPE.context)
    assert raised.value.category is PayloadErrorCategory.CODEC_FAILED
    _assert_without_sentinel(raised.value)
    assert created and created[0].cr_frame is None


def test_control_flow_exceptions_are_rebuilt_without_text(
    caplog: pytest.LogCaptureFixture,
) -> None:
    failures = _boundary_failures(InterruptingCodec(), AsyncInterruptingCodec())
    assert len(failures) == 4
    for failure in failures:
        assert type(failure) is KeyboardInterrupt
        assert failure.args == ()
        assert failure.__cause__ is None and failure.__context__ is None
    property_failures = _boundary_failures(
        SentinelCodec(InterruptingPropertyError()),
        AsyncSentinelCodec(InterruptingPropertyError()),
    )
    for property_failure in property_failures:
        assert isinstance(property_failure, PayloadEncryptionError)
        _assert_without_sentinel(property_failure)
        assert property_failure.category is PayloadErrorCategory.CODEC_FAILED
    with caplog.at_level(logging.DEBUG):
        sent = asyncio.run(
            _executed_worker_frames(
                PayloadEncryptionOptions(
                    SentinelCodec(InterruptingPropertyError()),
                    encrypt_error_text=False,
                )
            )
        )
    assert SENTINEL not in "".join(str(request) for request in sent)
    assert SENTINEL not in caplog.text


def test_task_cancellation_still_cancels_a_running_codec_call() -> None:
    started = asyncio.Event()

    class SlowCodec(AsyncCodec):
        async def decode(
            self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
        ) -> list[bytes]:
            started.set()
            await asyncio.sleep(30)
            return []

    async def scenario() -> bool:
        payloads = AsyncPayloadMessageCodec(PayloadEncryptionOptions(SlowCodec()))
        task = asyncio.create_task(
            payloads.decode_values([_encrypted_secret()], SCOPE.context)
        )
        await started.wait()
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        return task.cancelled()

    assert asyncio.run(scenario())


class UnreadableSignatureDecode:
    """A callable ``decode`` whose signature cannot be read."""

    def __init__(self, inner: AesGcmPayloadCodec) -> None:
        self.inner = inner

    @property
    def __signature__(self) -> Any:
        raise RuntimeError(SENTINEL)

    def __call__(
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> list[bytes]:
        return self.inner.decode(context, envelopes)


class AsyncUnreadableSignatureDecode(UnreadableSignatureDecode):
    async def __call__(  # type: ignore[override]
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> list[bytes]:
        return self.inner.decode(context, envelopes)


class AsyncEncodeCallable:
    def __init__(self, inner: AesGcmPayloadCodec) -> None:
        self.inner = inner

    async def __call__(
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> list[pb.PayloadEnvelope]:
        return self.inner.encode(context, plaintexts)


class CallableAttributeCodec:
    """A codec whose methods are callable instances, as some wrappers produce."""

    def __init__(self, encode: Any, decode: Any) -> None:
        self.encode = encode
        self.decode = decode


def test_unreadable_codec_signatures_and_async_callables_still_work() -> None:
    inner = AesGcmPayloadCodec(MemoryKeyProvider({"k1": KEY}, "k1"))
    sync_codec = CallableAttributeCodec(inner.encode, UnreadableSignatureDecode(inner))
    payloads = PayloadMessageCodec(PayloadEncryptionOptions(sync_codec))
    encrypted = payloads.encode_message(
        pb.WriteStreamRequest(value=pb.Value(string_value="hello")), SCOPE
    ).value
    assert payloads.decode_values([encrypted], SCOPE.context)[0].string_value == (
        "hello"
    )

    async def round_trip() -> str:
        async_codec = CallableAttributeCodec(
            AsyncEncodeCallable(inner), AsyncUnreadableSignatureDecode(inner)
        )
        async_payloads = AsyncPayloadMessageCodec(PayloadEncryptionOptions(async_codec))
        encoded = await async_payloads.encode_message(
            pb.WriteStreamRequest(value=pb.Value(string_value="hello")), SCOPE
        )
        decoded = await async_payloads.decode_values([encoded.value], SCOPE.context)
        return decoded[0].string_value

    assert asyncio.run(round_trip()) == "hello"
    with pytest.raises(TypeError, match="synchronous PayloadCodec"):
        PayloadMessageCodec(
            PayloadEncryptionOptions(
                CallableAttributeCodec(AsyncEncodeCallable(inner), inner.decode)
            )
        )


def test_flow_closed_history_event_error_message_is_a_read_point() -> None:
    payloads = PayloadMessageCodec(options())
    encrypted = payloads.encode_message(
        pb.StopFlowRequest(reason="closed with secret"), SCOPE
    ).reason
    event = pb.FlowClosedHistoryEvent(error_message=encrypted)
    decoded = payloads.decode_message(event, SCOPE)
    assert decoded.error_message == "closed with secret"


class StatusRpcError(grpc.RpcError):
    """A gRPC error with a status message and optional Dex error details."""

    def __init__(self, message: str, details: pb.ServiceErrorResponse | None) -> None:
        super().__init__()
        self._message = message
        packed = []
        if details is not None:
            packed_details = any_pb2.Any()
            packed_details.Pack(details)
            packed.append(packed_details)
        status = status_pb2.Status(
            code=grpc.StatusCode.UNKNOWN.value[0], message=message, details=packed
        )
        self._metadata = (("grpc-status-details-bin", status.SerializeToString()),)

    def code(self) -> grpc.StatusCode:
        return grpc.StatusCode.UNKNOWN

    def details(self) -> str:
        return self._message

    def trailing_metadata(self) -> tuple[Any, ...]:
        return self._metadata


def _encrypted_text(text: str) -> str:
    return (
        PayloadMessageCodec(options())
        .encode_message(pb.StopFlowRequest(reason=text), SCOPE)
        .reason
    )


def _invoke_rpc_failures(
    tmp_path: Path, rpc_error: grpc.RpcError, encryption: PayloadEncryptionOptions
) -> list[BaseException]:
    """Invoke an RPC whose call fails with ``rpc_error``, sync and async."""
    flow = PayloadRpcFlow()
    service_error = translate_rpc_error(rpc_error, "invoke_rpc", "order-1", "active")
    failures: list[BaseException] = []
    cache = open_blob_cache(BlobCacheConfig(str(tmp_path / "cache"), 1024 * 1024))
    try:
        client = Client(
            Registry([flow]), cache, ClientOptions(payload_encryption=encryption)
        )
        with patch.object(client, "_invoke_rpc_request", side_effect=service_error):
            try:
                client.invoke_rpc(flow.get_note, "order-1")
            except BaseException as error:
                failures.append(error)
        client.close()

        async def invoke_async() -> None:
            async_client = AsyncClient(
                Registry([flow]), cache, ClientOptions(payload_encryption=encryption)
            )
            try:
                with patch.object(
                    async_client,
                    "_invoke_rpc_request",
                    new_callable=AsyncMock,
                    side_effect=service_error,
                ):
                    await async_client.invoke_rpc(flow.get_note, "order-1")
            except BaseException as error:
                failures.append(error)
            finally:
                await async_client.close()

        asyncio.run(invoke_async())
    finally:
        cache.close()
    assert len(failures) == 2
    return failures


def _worker_failure_details(detail: str, worker_detail: str) -> pb.ServiceErrorResponse:
    return pb.ServiceErrorResponse(
        detail=detail,
        sub_status=pb.ERROR_SUB_STATUS_WORKER_API_ERROR,
        original_worker_error_type="builtins.RuntimeError",
        original_worker_error_detail=worker_detail,
    )


def test_invoke_rpc_decodes_the_status_message_of_dex_statuses(
    tmp_path: Path,
) -> None:
    rpc_error = StatusRpcError(
        _encrypted_text("status secret"),
        _worker_failure_details("", _encrypted_text("worker secret")),
    )
    for failure in _invoke_rpc_failures(tmp_path, rpc_error, options()):
        assert isinstance(failure, WorkerInvocationError)
        assert failure.detail == "status secret"
        assert failure.worker_error_detail == "worker secret"


def test_invoke_rpc_rejects_a_malformed_prefix_without_chaining_status_text(
    tmp_path: Path,
) -> None:
    malformed = ENCODED_TEXT_PREFIX + "SERVER_SECRET!"
    rpc_error = StatusRpcError(malformed, _worker_failure_details("", "plain"))
    for failure in _invoke_rpc_failures(tmp_path, rpc_error, options()):
        assert isinstance(failure, PayloadEncryptionError)
        assert failure.category is PayloadErrorCategory.MALFORMED
        assert failure.__cause__ is None and failure.__context__ is None
        assert "SERVER_SECRET" not in str(failure)


def test_invoke_rpc_codec_failure_chains_no_service_text(tmp_path: Path) -> None:
    rpc_error = StatusRpcError(
        "SERVER_SECRET",
        _worker_failure_details("SERVER_SECRET", _encrypted_text("secret")),
    )
    failing = PayloadEncryptionOptions(SentinelCodec(RuntimeError(SENTINEL)))
    for failure in _invoke_rpc_failures(tmp_path, rpc_error, failing):
        assert isinstance(failure, PayloadEncryptionError)
        assert failure.__cause__ is None and failure.__context__ is None
        assert "SERVER_SECRET" not in str(failure) and SENTINEL not in str(failure)


def test_invoke_rpc_leaves_statuses_without_dex_details_unchanged(
    tmp_path: Path,
) -> None:
    transport_text = _encrypted_text("not a read point")
    rpc_error = StatusRpcError(transport_text, None)
    for failure in _invoke_rpc_failures(tmp_path, rpc_error, options()):
        assert isinstance(failure, DexServiceError)
        assert failure.detail == transport_text


class PayloadRpcFlow(Flow[None]):
    def get_steps(self) -> StepList[None]:
        return StepList.empty()

    @rpc
    def get_note(self, context: Context) -> RPCResult[str]:
        return RPCResult("note")


class RetryingHostileDispatcher:
    async def invoke_rpc(
        self, request: pb.InvokeWorkerRPCRequest, is_active: Any
    ) -> pb.InvokeWorkerRPCResponse:
        raise retry_after(7, HostilePayloadError())


def test_retry_after_wrapping_a_payload_error_uses_fixed_fields(
    caplog: pytest.LogCaptureFixture,
) -> None:
    async def execute() -> list[Any]:
        runtime = PullWorkerRuntime(
            Registry([]),
            RetryingHostileDispatcher(),
            PullWorkerOptions(max_concurrent_tasks=1, app_worker_id="retry-worker"),
            "localhost:8801",
            None,
            frame_payloads=AsyncPayloadMessageCodec(options(encrypt_error_text=False)),
        )
        stream = PullStream(runtime, None, pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC)
        task = pb.PollWorkerTask(
            task_id="task",
            rpc=pb.InvokeWorkerRPCRequest(
                flow_type="OrderFlow", context=pb.Context(flow_id="order-1")
            ),
        )
        with patch.object(stream, "_send", new_callable=AsyncMock) as send_mock:
            await stream._execute(task)
        return [call.args[0] for call in send_mock.await_args_list]

    with caplog.at_level(logging.DEBUG):
        sent = asyncio.run(execute())
    worker_error = sent[-1].task_frame.worker_error
    assert worker_error.detail.startswith("payload codec_failed:")
    assert worker_error.stack_trace == ""
    assert worker_error.retry_after_seconds == 7
    assert SENTINEL not in "".join(str(request) for request in sent)
    assert SENTINEL not in caplog.text
