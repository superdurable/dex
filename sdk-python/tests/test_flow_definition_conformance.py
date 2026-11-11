# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import asyncio
import json
import threading
import time
from collections.abc import Iterator
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

import grpc
import pytest
from google.protobuf import empty_pb2

import dex._flow_definition_upload as upload_module
from dex import (
    AsyncWorker,
    BlobCacheConfig,
    Flow,
    FlowDefinitionDocument,
    PullWorkerOptions,
    Registry,
    Worker,
    WorkerOptions,
)
from dex._flow_definition_upload import (
    PreparedFlowDefinition,
    _InvalidFlowDefinition,
    encode_json_string,
    parse_flow_definition_content,
    parse_rfc3339_milliseconds,
    prepare_flow_definitions,
    rewrite_source_path,
    shareable_source_path,
    upload_worker_flow_definitions,
)
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc

CONFORMANCE = json.loads(
    (
        Path(__file__).resolve().parents[2]
        / "protos"
        / "conformance"
        / "flow_definition_upload.json"
    ).read_text(encoding="utf-8")
)


@pytest.mark.parametrize(
    "case", CONFORMANCE["sourcePathRewrite"], ids=lambda case: case["name"]
)
def test_source_path_rewrite_conformance(case: dict[str, Any]) -> None:
    assert shareable_source_path(case["sourcePath"], case["cwd"]) == case["expected"]


@pytest.mark.parametrize("case", CONFORMANCE["jsonStringEncoding"])
def test_json_string_encoding_conformance(case: dict[str, str]) -> None:
    assert encode_json_string(case["value"]) == case["encoded"]


@pytest.mark.parametrize(
    "case", CONFORMANCE["generatedAt"], ids=lambda case: case["value"]
)
def test_generated_at_conformance(case: dict[str, Any]) -> None:
    if case.get("error"):
        with pytest.raises(ValueError):
            parse_rfc3339_milliseconds(case["value"])
        return
    parsed = parse_rfc3339_milliseconds(case["value"])
    assert parsed.tzinfo is not None
    milliseconds = (parsed - datetime(1970, 1, 1, tzinfo=timezone.utc)) // timedelta(
        milliseconds=1
    )
    assert milliseconds == case["unixMillis"]


@pytest.mark.parametrize(
    "case", CONFORMANCE["documentValidity"], ids=lambda case: case["name"]
)
def test_document_validity_conformance(case: dict[str, Any]) -> None:
    content = case["document"].encode("utf-8")
    if case["valid"]:
        parse_flow_definition_content(content, "test")
        return
    with pytest.raises(ValueError) as failure:
        parse_flow_definition_content(content, "test")
    # A generatedAt error is a configuration error, never a skippable non-FDG file.
    assert isinstance(failure.value, _InvalidFlowDefinition) != bool(
        case.get("generatedAtError")
    )


@pytest.mark.parametrize(
    "case", CONFORMANCE["documentRewrite"], ids=lambda case: case["name"]
)
def test_document_rewrite_conformance(case: dict[str, Any]) -> None:
    content = case["document"].encode("utf-8")
    expected = content if case["expected"] is None else case["expected"].encode("utf-8")
    assert rewrite_source_path(content, case["cwd"]) == expected


def test_in_memory_document_with_malformed_generated_at_is_rejected() -> None:
    content = fdg("MemoryFlow", generatedAt="2026-11-01T00:00:00+00:99")
    with pytest.raises(ValueError, match="not RFC 3339"):
        prepare_flow_definitions(
            (),
            (
                FlowDefinitionDocument(
                    "MemoryFlow", content, datetime(2026, 11, 1, tzinfo=timezone.utc)
                ),
            ),
        )


def fdg(flow_type: str, schema_version: str = "2.0", **fields: Any) -> str:
    return json.dumps(
        {"schemaVersion": schema_version, "flow": {"name": flow_type}, **fields}
    )


def test_directory_uploads_only_flow_definition_2x(tmp_path: Path) -> None:
    (tmp_path / "package.json").write_text('{"name": "app", "version": "1.0.0"}')
    (tmp_path / "old.json").write_text(fdg("OldFlow", schema_version="1.0"))
    (tmp_path / "broken.json").write_text("{not json")
    (tmp_path / "notes.txt").write_text(fdg("TextFlow"))
    nested = tmp_path / "nested"
    nested.mkdir()
    (nested / "Upper.JSON").write_text(
        fdg("UpperFlow", generatedAt="2026-11-01T00:00:00.9999Z")
    )
    (tmp_path / "valid.json").write_text(fdg("ValidFlow"))
    prepared = prepare_flow_definitions((tmp_path,), ())
    assert sorted(definition.flow_type for definition in prepared) == [
        "UpperFlow",
        "ValidFlow",
    ]
    upper = next(
        definition for definition in prepared if definition.flow_type == "UpperFlow"
    )
    assert upper.generated_at == datetime(
        2026, 11, 1, 0, 0, 0, 999000, tzinfo=timezone.utc
    )
    assert all(
        definition.generated_at.microsecond % 1000 == 0 for definition in prepared
    )


def test_directory_flow_definition_with_bad_generated_at_is_an_error(
    tmp_path: Path,
) -> None:
    (tmp_path / "bad.json").write_text(
        fdg("BadFlow", generatedAt="2026-11-01 00:00:00Z")
    )
    with pytest.raises(ValueError, match="not RFC 3339"):
        prepare_flow_definitions((tmp_path,), ())


def test_explicit_file_must_be_flow_definition_2x(tmp_path: Path) -> None:
    explicit = tmp_path / "flow.data"
    explicit.write_text(fdg("ExplicitFlow"))
    assert [d.flow_type for d in prepare_flow_definitions((explicit,), ())] == [
        "ExplicitFlow"
    ]
    old = tmp_path / "old.json"
    old.write_text(fdg("OldFlow", schema_version="1.0"))
    with pytest.raises(ValueError, match="schemaVersion"):
        prepare_flow_definitions((old,), ())
    with pytest.raises(ValueError, match="does not exist"):
        prepare_flow_definitions((tmp_path / "missing.json",), ())


def test_in_memory_document_is_validated_and_truncated() -> None:
    generated_at = datetime(2026, 11, 1, 0, 0, 0, 123456, tzinfo=timezone.utc)
    content = fdg("MemoryFlow", source={"path": "/elsewhere/a.json", "language": "go"})
    (prepared,) = prepare_flow_definitions(
        (), (FlowDefinitionDocument("MemoryFlow", content, generated_at),)
    )
    assert prepared.generated_at == generated_at.replace(microsecond=123000)
    assert (
        prepared.content == content.replace('"/elsewhere/a.json"', '"a.json"').encode()
    )
    with pytest.raises(ValueError, match="schemaVersion"):
        prepare_flow_definitions(
            (),
            (
                FlowDefinitionDocument(
                    "MemoryFlow", fdg("MemoryFlow", "1.0"), generated_at
                ),
            ),
        )


def test_upload_batches_share_one_total_deadline(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    clock = [100.0]
    monkeypatch.setattr(upload_module, "monotonic", lambda: clock[0])
    definitions = [
        PreparedFlowDefinition(
            f"Flow{index}", b"{}", datetime(2026, 11, 1, tzinfo=timezone.utc), "test"
        )
        for index in range(600)
    ]
    timeouts: list[float] = []

    def upload(
        request: pb.UploadFlowDefinitionsRequest, timeout: float
    ) -> pb.UploadFlowDefinitionsResponse:
        timeouts.append(timeout)
        clock[0] += 4.0
        return pb.UploadFlowDefinitionsResponse()

    upload_worker_flow_definitions(
        upload,
        definitions,
        {definition.flow_type for definition in definitions},
        "test",
        10.0,
    )
    assert timeouts == [10.0, 6.0, 2.0]

    timeouts.clear()
    clock[0] = 100.0

    def slow_upload(
        request: pb.UploadFlowDefinitionsRequest, timeout: float
    ) -> pb.UploadFlowDefinitionsResponse:
        timeouts.append(timeout)
        clock[0] += 11.0
        return pb.UploadFlowDefinitionsResponse()

    upload_worker_flow_definitions(
        slow_upload,
        definitions,
        {definition.flow_type for definition in definitions},
        "test",
        10.0,
    )
    assert timeouts == [10.0]


class HangingUploadFlow(Flow[None]):
    def get_flow_type(self) -> str:
        return "HangingUploadFlow"


class MemoryBlobCache:
    config = BlobCacheConfig("memory", 1_024)

    def get(self, _blob_id: str) -> bytes | None:
        return None

    def put(self, _blob_id: str, _payload: bytes) -> bool:
        return True

    def delete(self, _blob_id: str) -> None:
        return None

    def delete_all(self) -> None:
        return None

    def close(self) -> None:
        return None


class HangingUploadService(
    dex_pb2_grpc.FlowServiceServicer, dex_pb2_grpc.WorkerTaskServiceServicer
):
    def __init__(self) -> None:
        self.upload_started = threading.Event()
        self.release = threading.Event()

    def GetServerInfo(  # noqa: N802
        self, request: empty_pb2.Empty, context: grpc.ServicerContext
    ) -> pb.ServerInfo:
        return pb.ServerInfo(
            server_version="test",
            minimum_supported_protocol_version=1,
            current_protocol_version=4,
        )

    def SyncAttributeIndexes(  # noqa: N802
        self, request: pb.SyncAttributeIndexRequest, context: grpc.ServicerContext
    ) -> pb.SyncAttributeIndexResponse:
        return pb.SyncAttributeIndexResponse()

    def UploadFlowDefinitions(  # noqa: N802
        self, request: pb.UploadFlowDefinitionsRequest, context: grpc.ServicerContext
    ) -> pb.UploadFlowDefinitionsResponse:
        self.upload_started.set()
        self.release.wait(timeout=30)
        return pb.UploadFlowDefinitionsResponse()

    def PollWorkerTask(  # noqa: N802
        self,
        request_iterator: Iterator[pb.PollWorkerTaskRequest],
        context: grpc.ServicerContext,
    ) -> Iterator[pb.PollWorkerTaskResponse]:
        next(request_iterator)
        yield pb.PollWorkerTaskResponse(registered=pb.PollWorkerTaskRegistered())
        for _ in request_iterator:
            pass


def start_hanging_server() -> tuple[grpc.Server, int, HangingUploadService]:
    service = HangingUploadService()
    server = grpc.server(ThreadPoolExecutor(max_workers=6))
    dex_pb2_grpc.add_FlowServiceServicer_to_server(service, server)  # type: ignore[no-untyped-call]
    dex_pb2_grpc.add_WorkerTaskServiceServicer_to_server(service, server)  # type: ignore[no-untyped-call]
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    return server, port, service


def hanging_worker_options(port: int) -> WorkerOptions:
    return WorkerOptions(
        server_address=f"127.0.0.1:{port}",
        pull=PullWorkerOptions(max_concurrent_tasks=2),
        flow_definitions=(
            FlowDefinitionDocument(
                "HangingUploadFlow",
                fdg("HangingUploadFlow"),
                datetime(2026, 11, 1, tzinfo=timezone.utc),
            ),
        ),
    )


def test_worker_stop_cancels_in_flight_upload() -> None:
    server, port, service = start_hanging_server()
    worker = Worker(
        Registry((HangingUploadFlow(),)),
        MemoryBlobCache(),
        hanging_worker_options(port),
    )
    failures: list[BaseException] = []

    def run_worker() -> None:
        try:
            worker.start()
        except BaseException as failure:
            failures.append(failure)

    thread = threading.Thread(target=run_worker)
    thread.start()
    try:
        assert service.upload_started.wait(timeout=5)
        stop_started = time.monotonic()
        worker.stop()
        thread.join(timeout=5)
        assert time.monotonic() - stop_started < 5
        assert not thread.is_alive()
        assert failures == []
    finally:
        service.release.set()
        server.stop(grace=None).wait(timeout=5)


def test_async_worker_stop_cancels_in_flight_upload() -> None:
    server, port, service = start_hanging_server()

    async def run() -> None:
        worker = AsyncWorker(
            Registry((HangingUploadFlow(),), allow_async_handlers=True),
            MemoryBlobCache(),
            hanging_worker_options(port),
        )
        start_task = asyncio.create_task(worker.start())
        while not service.upload_started.is_set():
            await asyncio.sleep(0.01)
        await asyncio.wait_for(worker.stop(), timeout=5)
        await asyncio.wait_for(start_task, timeout=5)

    try:
        asyncio.run(run())
    finally:
        service.release.set()
        server.stop(grace=None).wait(timeout=5)
