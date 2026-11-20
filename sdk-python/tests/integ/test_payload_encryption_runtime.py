# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

"""Payload encryption against a real server-zd, for the sync and async SDKs."""

from __future__ import annotations

import asyncio
import base64
import json
import os
import threading
import time
from collections.abc import Callable, Sequence
from datetime import timedelta
from pathlib import Path
from tempfile import TemporaryDirectory
from typing import TypeVar

import pytest

from dex import (
    WORK_QUEUE_PERMISSIONS_INDEX_KEY,
    AesGcmPayloadCodec,
    AttributeMatch,
    BlobCacheConfig,
    Client,
    ClientOptions,
    CloneForkFlowOptions,
    FilePayloadEncryptionKeyProvider,
    FlowStatus,
    PayloadContext,
    PayloadEncryptionOptions,
    Registry,
    StopFlowOptions,
    TimeTravelOptions,
    TimeTravelType,
    ValueMappingError,
    WaitForAttributeOptions,
    Worker,
    WorkerInvocationError,
    WorkerOptions,
    open_blob_cache,
)
from dex._payload_message_codec import PayloadMessageCodec, PayloadScope
from dex.dexpb import dex_pb2 as pb

from .async_environment import AsyncDexDevTestEnvironment
from .environment import DexDevTestEnvironment
from .flow_service_client import flow_service_client
from .payload_encryption_flow import (
    APPROVE_PERMISSION,
    AWAITING_APPROVAL,
    SEARCH_TAG_KEY,
    SECRET_APPROVAL,
    SECRET_CHILD_FAILURE,
    SECRET_LARGE,
    SECRET_MAP_NOTE,
    SECRET_NOTE,
    SECRET_RECOVERY_DETAIL,
    SECRET_RETRY_DETAIL,
    SECRET_RPC_FAILURE,
    SECRET_STREAM,
    EncryptedActionConditionFlow,
    EncryptedOrder,
    EncryptedOrderSummary,
    PayloadEncryptionChildFlow,
    PayloadEncryptionOrderFlow,
    PlaintextOrderFlow,
    ReplayCounterFlow,
)
from .shared import unique_id

ResultT = TypeVar("ResultT")
WAIT = timedelta(seconds=60)
SERVER_ADDRESS = os.environ.get("DEX_SERVER_ADDRESS", "127.0.0.1:8801")
KEY_1 = base64.b64encode(bytes(range(32))).decode()
KEY_2 = base64.b64encode(bytes(range(32, 64))).decode()
TENANT_B_KEY = base64.b64encode(bytes(range(64, 96))).decode()


class RecordingPayloadCodec:
    """Wrap the built-in codec and record the context of every codec call."""

    def __init__(self, path: Path) -> None:
        self.inner = AesGcmPayloadCodec(FilePayloadEncryptionKeyProvider(path))
        self.contexts: list[tuple[str, PayloadContext]] = []
        self._lock = threading.Lock()

    def encode(
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> list[pb.PayloadEnvelope]:
        with self._lock:
            self.contexts.append(("encode", context))
        return self.inner.encode(context, plaintexts)

    def decode(
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> list[bytes]:
        with self._lock:
            self.contexts.append(("decode", context))
        return self.inner.decode(context, envelopes)


class TenantKeyProvider:
    """Refuse keys of another tenant; the tenant is the Flow ID prefix."""

    def __init__(self, path: Path) -> None:
        self.inner = FilePayloadEncryptionKeyProvider(path)

    def current_key_id(self, context: PayloadContext) -> str:
        return context.flow_id.split("-")[0]

    def key(self, context: PayloadContext, key_id: str) -> bytes:
        if not context.flow_id.startswith(key_id + "-"):
            raise KeyError(key_id)
        return self.inner.key(context, key_id)


class AsyncAesGcmPayloadCodec:
    def __init__(self, inner: AesGcmPayloadCodec) -> None:
        self.inner = inner

    async def encode(
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> list[pb.PayloadEnvelope]:
        await asyncio.sleep(0)
        return self.inner.encode(context, plaintexts)

    async def decode(
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> list[bytes]:
        await asyncio.sleep(0)
        return self.inner.decode(context, envelopes)


def write_key_file(directory: Path, current: str, keys: dict[str, str]) -> Path:
    path = directory / f"keys-{current}.json"
    path.write_text(json.dumps({"current_key_id": current, "keys": keys}))
    path.chmod(0o600)
    return path


def file_encryption(path: Path) -> PayloadEncryptionOptions:
    return PayloadEncryptionOptions(
        AesGcmPayloadCodec(FilePayloadEncryptionKeyProvider(path))
    )


def order_flows() -> tuple[PayloadEncryptionOrderFlow, PayloadEncryptionChildFlow]:
    child = PayloadEncryptionChildFlow()
    return PayloadEncryptionOrderFlow(child), child


def eventually(read: Callable[[], ResultT | None], timeout: float = 30) -> ResultT:
    deadline = time.monotonic() + timeout
    while True:
        result = read()
        if result is not None:
            return result
        if time.monotonic() > deadline:
            raise AssertionError("condition was not met in time")
        time.sleep(0.2)


def history_bytes(flow_id: str) -> bytes:
    response = flow_service_client().GetHistoryEvents(
        pb.GetHistoryEventsRequest(flow_id=flow_id, estimate_page_size=1000)
    )
    return bytes(response.SerializeToString())


def raw_attributes(flow_id: str, *keys: str) -> dict[str, pb.Value]:
    response = flow_service_client().GetAttributes(
        pb.GetAttributesRequest(flow_id=flow_id, keys=keys)
    )
    return {entry.key: entry.value for entry in response.attributes}


def envelope_key_id(value: pb.Value) -> str:
    assert value.obj_value.encoding == "dex/1"
    return pb.PayloadEnvelope.FromString(value.obj_value.payload).key_id


def raw_last_failure_detail(flow_id: str) -> str | None:
    response = flow_service_client().GetFlowState(
        pb.GetFlowStateRequest(flow_id=flow_id)
    )
    for step in response.active_step_executions:
        if step.HasField("last_failure_info"):
            details = step.last_failure_info.details
            return str(details.original_worker_error_detail or details.detail)
    return None


def test_sync_sdk_encrypts_every_path_and_server_keeps_only_ciphertext(
    tmp_path: Path,
) -> None:
    key_file = write_key_file(tmp_path, "k1", {"k1": KEY_1})
    recording_codec = RecordingPayloadCodec(key_file)
    encryption = PayloadEncryptionOptions(recording_codec)
    order_flow, child_flow = order_flows()
    with DexDevTestEnvironment(
        order_flow, child_flow, payload_encryption=encryption
    ) as environment:
        client = environment.client
        flow_id = unique_id("payload-encryption-sync")
        tag = unique_id("tag")
        client.start_flow(order_flow, flow_id, EncryptedOrder("SECRET-card", tag))

        # Error text: the Server holds the prefix form, get_flow_state decrypts it.
        raw_detail = eventually(lambda: raw_last_failure_detail(flow_id))
        assert raw_detail.startswith("dex-encoded:")
        failures = [
            step.last_failure
            for step in client.get_flow_state(flow_id).active_step_executions
            if step.last_failure is not None
        ]
        assert failures and failures[0].detail == SECRET_RETRY_DETAIL

        # A plaintext Attribute works with wait_for_attribute_match.
        status = client.wait_for_attribute_match(
            flow_id,
            order_flow.status,
            AttributeMatch.equal_to(AWAITING_APPROVAL),
            WaitForAttributeOptions(request_timeout=WAIT),
        )
        assert status == AWAITING_APPROVAL
        with pytest.raises(ValueError, match="plaintext=True"):
            client.wait_for_attribute_match(
                flow_id,
                order_flow.note,
                AttributeMatch.equal_to(SECRET_NOTE),
                WaitForAttributeOptions(request_timeout=WAIT),
            )

        snapshot = client.invoke_rpc(order_flow.get_snapshot, flow_id)
        assert (snapshot.note, snapshot.count, snapshot.large) == (
            SECRET_NOTE,
            42,
            SECRET_LARGE,
        )

        # The large value is a blob of ciphertext; the second read hits the cache.
        raw = raw_attributes(flow_id, order_flow.large.name, order_flow.note.name)
        assert raw[order_flow.large.name].WhichOneof("kind") == (
            "internal_blob_id_for_obj_value"
        )
        assert raw[order_flow.note.name].obj_value.encoding == "dex/1"
        reads = (
            order_flow.note,
            order_flow.count,
            order_flow.large,
            order_flow.status,
            order_flow.map_notes.instance("first"),
        )
        for _ in range(2):
            values = client.get_attributes(flow_id, *reads)
            assert values[order_flow.note] == SECRET_NOTE
            assert values[order_flow.count] == 42
            assert values[order_flow.large] == SECRET_LARGE
            assert values[order_flow.status] == AWAITING_APPROVAL
            assert values[order_flow.map_notes.instance("first")] == SECRET_MAP_NOTE
        blob_id = raw[order_flow.large.name].internal_blob_id_for_obj_value
        cached = environment._cache.get(f"{len(flow_id)}:{flow_id}{blob_id}")
        assert cached is not None and b"SECRET" not in cached

        assert client.read_stream(flow_id, order_flow.progress).value == SECRET_STREAM
        page = client.list_stream_messages(flow_id, order_flow.progress, 10)
        assert [message.value for message in page.messages] == [SECRET_STREAM]

        # The indexed Attribute stays plaintext and searchable; so do Server values.
        entry = eventually(
            lambda: next(
                iter(client.search_flows(f"{SEARCH_TAG_KEY} = '{tag}'", 10).flows),
                None,
            )
        )
        assert entry.indexed_attributes[SEARCH_TAG_KEY].data == tag
        permissions = entry.indexed_attributes[WORK_QUEUE_PERMISSIONS_INDEX_KEY]
        assert APPROVE_PERMISSION in str(permissions.data)

        with pytest.raises(WorkerInvocationError) as rpc_failure:
            client.invoke_rpc(order_flow.fail_with_secret, flow_id)
        assert rpc_failure.value.worker_error_detail == SECRET_RPC_FAILURE

        client.invoke_rpc(order_flow.approve, flow_id, SECRET_APPROVAL)
        result = client.wait_for_flow(flow_id, WAIT)
        assert result.status is FlowStatus.COMPLETED
        summary = result.single_output(EncryptedOrderSummary)
        child_flow_id = summary.child_flow_id
        assert child_flow_id.startswith(f"SubFlow:{flow_id}-RunChildrenStep-")
        assert summary.approval == SECRET_APPROVAL
        assert summary.card_number == "SECRET-card"
        assert summary.child_output == (
            f"child:{SECRET_APPROVAL}|SECRET-card:{child_flow_id}"
        )
        assert summary.child_error == SECRET_CHILD_FAILURE
        assert summary.recovery_detail == SECRET_RECOVERY_DETAIL

        # The parent encrypts the SubFlow input with the real child Flow ID, and
        # the child Worker decrypts it with the same context.
        child_context = PayloadContext(child_flow.get_flow_type(), child_flow_id)
        assert ("encode", child_context) in recording_codec.contexts
        assert ("decode", child_context) in recording_codec.contexts
        # A parent reads the SubFlow result by the child Flow ID only.
        assert ("decode", PayloadContext("", child_flow_id)) in (
            recording_codec.contexts
        )

        failed_child_id = child_flow_id[: -len("0")] + "1"
        for stored_flow_id in (flow_id, child_flow_id, failed_child_id):
            stored = history_bytes(stored_flow_id)
            assert b"dex/1" in stored or b"dex-encoded:" in stored
            assert b"SECRET" not in stored


def test_async_sdk_with_async_codec_round_trips_and_hides_payloads(
    tmp_path: Path,
) -> None:
    key_file = write_key_file(tmp_path, "k1", {"k1": KEY_1})
    codec = AsyncAesGcmPayloadCodec(
        AesGcmPayloadCodec(FilePayloadEncryptionKeyProvider(key_file))
    )
    encryption = PayloadEncryptionOptions(codec)
    child_flow = PayloadEncryptionChildFlow()
    order_flow = PayloadEncryptionOrderFlow(child_flow, is_async=True)

    async def scenario() -> None:
        async with AsyncDexDevTestEnvironment(
            order_flow,
            child_flow,
            allow_async_handlers=True,
            payload_encryption=encryption,
        ) as environment:
            client = environment.client
            flow_id = unique_id("payload-encryption-async")
            await client.start_flow(
                order_flow, flow_id, EncryptedOrder("SECRET-card", unique_id("tag"))
            )
            status = await client.wait_for_attribute_match(
                flow_id,
                order_flow.status,
                AttributeMatch.equal_to(AWAITING_APPROVAL),
                WaitForAttributeOptions(request_timeout=WAIT),
            )
            assert status == AWAITING_APPROVAL
            values = await client.get_attributes(
                flow_id, order_flow.note, order_flow.large
            )
            assert values[order_flow.note] == SECRET_NOTE
            assert values[order_flow.large] == SECRET_LARGE
            stream = await client.read_stream(flow_id, order_flow.progress)
            assert stream.value == SECRET_STREAM
            with pytest.raises(WorkerInvocationError) as rpc_failure:
                await client.invoke_rpc(order_flow.fail_with_secret, flow_id)
            assert rpc_failure.value.worker_error_detail == SECRET_RPC_FAILURE
            await client.invoke_rpc(order_flow.approve, flow_id, SECRET_APPROVAL)
            result = await client.wait_for_flow(flow_id, WAIT)
            summary = result.single_output(EncryptedOrderSummary)
            assert summary.child_error == SECRET_CHILD_FAILURE
            assert summary.recovery_detail == SECRET_RECOVERY_DETAIL
            state = await client.get_flow_state(flow_id)
            assert state.active_step_executions == ()
            assert b"SECRET" not in history_bytes(flow_id)

    asyncio.run(scenario())


def test_reasons_and_force_fail_text_are_encrypted(tmp_path: Path) -> None:
    encryption = file_encryption(write_key_file(tmp_path, "k1", {"k1": KEY_1}))
    order_flow, child_flow = order_flows()
    with DexDevTestEnvironment(
        order_flow, child_flow, payload_encryption=encryption
    ) as environment:
        client = environment.client
        failing_id = unique_id("payload-encryption-force-fail")
        client.start_flow(child_flow, failing_id, "fail")
        result = client.wait_for_flow(failing_id, WAIT)
        assert result.status is FlowStatus.FAILED
        assert result.error_message == SECRET_CHILD_FAILURE
        raw = flow_service_client().WaitForFlow(
            pb.WaitForFlowRequest(flow_id=failing_id, needs_results=True)
        )
        assert raw.error_message.startswith("dex-encoded:")

        stopped_id = unique_id("payload-encryption-stop")
        client.start_flow(order_flow, stopped_id, EncryptedOrder("SECRET-c", "t"))
        client.stop_flow(stopped_id, StopFlowOptions(reason="SECRET-stop-reason"))
        assert client.wait_for_flow(stopped_id, WAIT).status is FlowStatus.CANCELED
        assert b"SECRET" not in history_bytes(stopped_id)


def test_clone_fork_and_time_travel_decrypt_copied_values(tmp_path: Path) -> None:
    encryption = file_encryption(write_key_file(tmp_path, "k1", {"k1": KEY_1}))
    order_flow, child_flow = order_flows()
    with DexDevTestEnvironment(
        order_flow, child_flow, payload_encryption=encryption
    ) as environment:
        client = environment.client
        flow_id = unique_id("payload-encryption-clone-source")
        client.start_flow(order_flow, flow_id, EncryptedOrder("SECRET-card", "t"))
        client.wait_for_attribute_match(
            flow_id,
            order_flow.status,
            AttributeMatch.equal_to(AWAITING_APPROVAL),
            WaitForAttributeOptions(request_timeout=WAIT),
        )
        clone_id = unique_id("payload-encryption-clone")
        client.clone_fork_flow(
            order_flow,
            flow_id,
            clone_id,
            CloneForkFlowOptions(reason="SECRET-clone-reason").with_attribute(
                order_flow.count, 7
            ),
        )
        values = client.get_attributes(clone_id, order_flow.note, order_flow.count)
        assert (values[order_flow.note], values[order_flow.count]) == (SECRET_NOTE, 7)
        assert (
            envelope_key_id(
                raw_attributes(clone_id, order_flow.count.name)[order_flow.count.name]
            )
            == "k1"
        )
        client.invoke_rpc(order_flow.approve, clone_id, SECRET_APPROVAL)
        clone_summary = client.wait_for_flow(clone_id, WAIT).single_output(
            EncryptedOrderSummary
        )
        assert clone_summary.card_number == "SECRET-card"

        client.time_travel(
            flow_id,
            TimeTravelOptions(
                type=TimeTravelType.STEP_TYPE,
                step_type="AwaitApprovalStep",
                reason="SECRET-time-travel-reason",
            ),
        )
        client.invoke_rpc(order_flow.approve, flow_id, SECRET_APPROVAL)
        summary = client.wait_for_flow(flow_id, WAIT).single_output(
            EncryptedOrderSummary
        )
        assert summary.card_number == "SECRET-card"
        assert b"SECRET" not in history_bytes(flow_id)
        assert b"SECRET" not in history_bytes(clone_id)


def test_key_rotation_reads_old_values_and_writes_with_the_new_key(
    tmp_path: Path,
) -> None:
    flow = PlaintextOrderFlow()
    old_keys = write_key_file(tmp_path, "k1", {"k1": KEY_1})
    with DexDevTestEnvironment(
        flow, payload_encryption=file_encryption(old_keys)
    ) as environment:
        old_id = unique_id("payload-encryption-rotation-old")
        environment.client.start_flow(flow, old_id, "SECRET-old")
        environment.client.wait_for_flow(old_id, WAIT)
    rotated_keys = write_key_file(tmp_path, "k2", {"k1": KEY_1, "k2": KEY_2})
    with DexDevTestEnvironment(
        flow, payload_encryption=file_encryption(rotated_keys)
    ) as environment:
        client = environment.client
        assert client.get_attributes(old_id, flow.note)[flow.note] == "SECRET-old"
        new_id = unique_id("payload-encryption-rotation-new")
        client.start_flow(flow, new_id, "SECRET-new")
        assert client.wait_for_flow(new_id, WAIT).single_output(str) == "SECRET-new"
        old_value = raw_attributes(old_id, flow.note.name)[flow.note.name]
        new_value = raw_attributes(new_id, flow.note.name)[flow.note.name]
        assert (envelope_key_id(old_value), envelope_key_id(new_value)) == (
            "k1",
            "k2",
        )


def test_worker_runs_plaintext_and_encrypted_flows_and_codecless_reads_fail(
    tmp_path: Path,
) -> None:
    flow = PlaintextOrderFlow()
    encryption = file_encryption(write_key_file(tmp_path, "k1", {"k1": KEY_1}))
    with DexDevTestEnvironment(flow, payload_encryption=encryption) as environment:
        with TemporaryDirectory() as cache_directory:
            cache = open_blob_cache(BlobCacheConfig(cache_directory, 1 << 20))
            plain_flow = PlaintextOrderFlow()
            try:
                with Client(
                    Registry((plain_flow,)),
                    cache,
                    ClientOptions(SERVER_ADDRESS),
                ) as plain_client:
                    # A codec-less Client writes plaintext input; the Worker reads it.
                    plain_id = unique_id("payload-encryption-plain")
                    plain_client.start_flow(plain_flow, plain_id, "plain-text")
                    plain_result = environment.client.wait_for_flow(plain_id, WAIT)
                    assert plain_result.single_output(str) == "plain-text"
                    assert b"plain-text" in history_bytes(plain_id)

                    encrypted_id = unique_id("payload-encryption-mixed")
                    environment.client.start_flow(flow, encrypted_id, "SECRET-mixed")
                    encrypted_result = environment.client.wait_for_flow(
                        encrypted_id, WAIT
                    )
                    assert encrypted_result.single_output(str) == "SECRET-mixed"
                    with pytest.raises(ValueMappingError) as missing_codec:
                        plain_client.get_attributes(encrypted_id, plain_flow.note)
                    message = str(missing_codec.value)
                    assert "value is encrypted; configure a PayloadCodec" in message
                    assert "SECRET" not in message
                    # The result fails when it is opened, before typed decoding.
                    with pytest.raises(ValueMappingError, match="PayloadCodec"):
                        plain_client.wait_for_flow(encrypted_id, WAIT)
            finally:
                cache.close()


def test_worker_rejects_action_condition_on_encrypted_attribute(
    tmp_path: Path,
) -> None:
    encryption = file_encryption(write_key_file(tmp_path, "k1", {"k1": KEY_1}))
    with TemporaryDirectory() as cache_directory:
        cache = open_blob_cache(BlobCacheConfig(cache_directory, 1 << 20))
        try:
            with pytest.raises(ValueError, match="plaintext=True"):
                Worker(
                    Registry((EncryptedActionConditionFlow(),)),
                    cache,
                    WorkerOptions(
                        server_address=SERVER_ADDRESS, payload_encryption=encryption
                    ),
                )
        finally:
            cache.close()


def test_tenant_key_refusal_fails_the_step_without_revealing_data(
    tmp_path: Path,
) -> None:
    flow = PlaintextOrderFlow()
    keys = write_key_file(
        tmp_path, "tenantb", {"tenanta": KEY_1, "tenantb": TENANT_B_KEY}
    )
    worker_encryption = PayloadEncryptionOptions(
        AesGcmPayloadCodec(TenantKeyProvider(keys))
    )
    with DexDevTestEnvironment(
        flow, payload_encryption=worker_encryption
    ) as environment:
        with TemporaryDirectory() as cache_directory:
            cache = open_blob_cache(BlobCacheConfig(cache_directory, 1 << 20))
            tenant_b_flow = PlaintextOrderFlow()
            try:
                with Client(
                    Registry((tenant_b_flow,)),
                    cache,
                    ClientOptions(
                        SERVER_ADDRESS, payload_encryption=file_encryption(keys)
                    ),
                ) as tenant_b_client:
                    flow_id = unique_id("tenanta")
                    tenant_b_client.start_flow(
                        tenant_b_flow, flow_id, "SECRET-tenant-b"
                    )
                    detail = eventually(lambda: raw_last_failure_detail(flow_id))
                    assert "unknown_key" in detail
                    assert "SECRET" not in detail
                    environment.client.stop_flow(flow_id)
            finally:
                cache.close()


def test_invoke_rpc_replay_with_new_ciphertext_runs_the_handler_once(
    tmp_path: Path,
) -> None:
    flow = ReplayCounterFlow()
    encryption = file_encryption(write_key_file(tmp_path, "k1", {"k1": KEY_1}))
    with DexDevTestEnvironment(flow, payload_encryption=encryption) as environment:
        flow_id = unique_id("payload-encryption-replay")
        environment.client.start_flow(flow, flow_id, None)
        rpc_name = environment.client.registry._rpc_for_method(flow.record_visit)[
            1
        ].name
        scope = PayloadScope(PayloadContext(flow.get_flow_type(), flow_id))
        request_id = unique_id("replay-request")
        outputs: list[int] = []
        sent_inputs: list[bytes] = []
        # Two Clients encrypt the same retry separately, so the bytes differ.
        for _ in range(2):
            payloads = PayloadMessageCodec(encryption)
            request = payloads.encode_message(
                pb.InvokeRPCRequest(
                    flow_id=flow_id,
                    rpc_name=rpc_name,
                    input=pb.Value(string_value="SECRET-visit"),
                    request_id=request_id,
                ),
                scope,
            )
            sent_inputs.append(request.input.SerializeToString())
            response = flow_service_client().InvokeRPC(request)
            output = payloads.decode_values([response.output], scope.context)[0]
            outputs.append(output.int_value)
        assert sent_inputs[0] != sent_inputs[1]
        assert outputs == [1, 1]
        values = environment.client.get_attributes(flow_id, flow.visits)
        assert values[flow.visits] == 1
        environment.client.stop_flow(flow_id)
