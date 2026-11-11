# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

import asyncio
import json
import logging
import os
import threading
from collections.abc import Iterator
from datetime import datetime, timedelta, timezone
from pathlib import Path
from tempfile import TemporaryDirectory
from uuid import uuid4

import grpc
import pytest

from dex import (
    AsyncContext,
    AsyncWorker,
    BlobCache,
    BlobCacheConfig,
    Client,
    ClientOptions,
    Flow,
    FlowDefinitionDocument,
    FlowDefinitionUploadOutcome,
    Registry,
    Step,
    StepDecision,
    StepList,
    Worker,
    WorkerOptions,
    graceful_complete,
    open_blob_cache,
)
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc

from .basic_flow import BasicFlow

SERVER_ADDRESS = os.environ.get("DEX_SERVER_ADDRESS", "127.0.0.1:8801")
GENERATED_AT = datetime(2026, 11, 1, 12, 30, tzinfo=timezone.utc)


class UploadedBasicFlow(BasicFlow):
    def __init__(self, flow_type: str) -> None:
        super().__init__()
        self.flow_type = flow_type

    def get_flow_type(self) -> str:
        return self.flow_type


class UploadedAsyncStep(Step[None]):
    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: None
    ) -> StepDecision:
        return graceful_complete()


class UploadedAsyncFlow(Flow[None]):
    def __init__(self, flow_type: str) -> None:
        self.flow_type = flow_type
        self.start = UploadedAsyncStep()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start)

    def get_flow_type(self) -> str:
        return self.flow_type


@pytest.fixture
def blob_cache() -> Iterator[BlobCache]:
    with TemporaryDirectory(prefix="dex-python-flow-definition-cache-") as directory:
        cache = open_blob_cache(BlobCacheConfig(directory, 64 * 1024 * 1024))
        try:
            yield cache
        finally:
            cache.close()


def test_worker_uploads_registered_definitions_from_paths_and_memory(
    blob_cache: BlobCache,
) -> None:
    from_file = unique_flow_type("PyFileFlow")
    from_memory = unique_flow_type("PyMemoryFlow")
    unregistered = unique_flow_type("PyUnregisteredFlow")
    with TemporaryDirectory(dir=os.getcwd()) as directory:
        nested = Path(directory) / "nested"
        nested.mkdir()
        absolute_source = str(Path(directory) / "workflow.py")
        file_content = flow_definition_json(
            from_file, absolute_source, generated_at="2026-10-01T08:00:00Z"
        )
        (nested / "file-flow.json").write_bytes(file_content)
        (nested / "ignored.txt").write_text("not a definition")
        (Path(directory) / "unregistered.json").write_bytes(
            flow_definition_json(unregistered, "workflow.py")
        )
        memory_content = flow_definition_json(
            from_memory, "/elsewhere/agent/session.ts"
        )
        registry = Registry(
            [UploadedBasicFlow(from_file), UploadedBasicFlow(from_memory)]
        )
        options = WorkerOptions(
            server_address=SERVER_ADDRESS,
            flow_definition_paths=(directory,),
            flow_definitions=(
                FlowDefinitionDocument(
                    from_memory, memory_content.decode(), GENERATED_AT
                ),
            ),
        )
        with running_worker(registry, blob_cache, options):
            stored = get_flow_definitions([from_file, from_memory, unregistered])

    assert set(stored) == {from_file, from_memory}
    expected_relative = os.path.relpath(absolute_source, os.getcwd())
    assert stored[from_file].content == file_content.replace(
        json.dumps(absolute_source).encode(), json.dumps(expected_relative).encode()
    )
    assert json.loads(stored[from_file].content)["source"]["path"] == expected_relative
    assert stored[from_file].info.generated_at.ToDatetime(tzinfo=timezone.utc) == (
        datetime(2026, 10, 1, 8, tzinfo=timezone.utc)
    )
    assert stored[from_file].info.uploader.startswith("sdk-python/")
    assert stored[from_memory].content == memory_content.replace(
        b'"/elsewhere/agent/session.ts"', b'"session.ts"'
    )
    assert stored[from_memory].info.generated_at.ToDatetime(tzinfo=timezone.utc) == (
        GENERATED_AT
    )


def test_async_worker_uploads_in_memory_definition(blob_cache: BlobCache) -> None:
    flow_type = unique_flow_type("PyAsyncMemoryFlow")
    content = flow_definition_json(flow_type, "session.py")

    async def run_worker() -> None:
        worker = AsyncWorker(
            Registry([UploadedAsyncFlow(flow_type)], allow_async_handlers=True),
            blob_cache,
            WorkerOptions(
                server_address=SERVER_ADDRESS,
                flow_definitions=(
                    FlowDefinitionDocument(flow_type, content, GENERATED_AT),
                ),
            ),
        )
        start_task = asyncio.create_task(worker.start())
        try:
            await worker.wait_pull_registered(timeout=30)
        finally:
            await worker.close()
            await start_task

    asyncio.run(run_worker())
    stored = get_flow_definitions([flow_type])
    assert stored[flow_type].content == content


def test_rejected_upload_does_not_stop_worker(
    blob_cache: BlobCache, caplog: pytest.LogCaptureFixture
) -> None:
    flow_type = unique_flow_type("PyRejectedFlow")
    rejected = json.dumps(
        {"schemaVersion": "1.0", "flow": {"name": flow_type}}
    ).encode()
    flow = UploadedBasicFlow(flow_type)
    registry = Registry([flow])
    options = WorkerOptions(
        server_address=SERVER_ADDRESS,
        flow_definitions=(FlowDefinitionDocument(flow_type, rejected, GENERATED_AT),),
    )
    with caplog.at_level(logging.WARNING, logger="dex._flow_definition_upload"):
        with running_worker(registry, blob_cache, options):
            with Client(registry, blob_cache, ClientOptions(SERVER_ADDRESS)) as client:
                flow_id = f"rejected-definition-{uuid4()}"
                client.start_flow(flow, flow_id, 10)
                assert client.wait_for_flow(flow_id).single_output(int) == 12
    assert get_flow_definitions([flow_type]) == {}
    assert any(
        "Flow definition upload failed" in record.getMessage()
        for record in caplog.records
    )


def test_duplicate_flow_type_across_sources_fails_worker_construction(
    blob_cache: BlobCache,
) -> None:
    flow_type = unique_flow_type("PyDuplicateFlow")
    with TemporaryDirectory() as directory:
        path = Path(directory) / "flow.json"
        path.write_bytes(flow_definition_json(flow_type, "workflow.py"))
        options = WorkerOptions(
            server_address=SERVER_ADDRESS,
            flow_definition_paths=(path,),
            flow_definitions=(
                FlowDefinitionDocument(
                    flow_type, flow_definition_json(flow_type, "x.py"), GENERATED_AT
                ),
            ),
        )
        with pytest.raises(ValueError, match="configured twice"):
            Worker(Registry([UploadedBasicFlow(flow_type)]), blob_cache, options)


def test_client_upload_reports_outcomes_and_force_replaces(
    blob_cache: BlobCache,
) -> None:
    flow_type = unique_flow_type("PyClientFlow")
    first = flow_definition_json(flow_type, "first.py")
    second = flow_definition_json(flow_type, "second.py")
    registry = Registry([UploadedBasicFlow(flow_type)])
    with Client(registry, blob_cache, ClientOptions(SERVER_ADDRESS)) as client:
        created = client.upload_flow_definitions(
            [FlowDefinitionDocument(flow_type, first, GENERATED_AT)]
        )
        unchanged = client.upload_flow_definitions(
            [FlowDefinitionDocument(flow_type, first, GENERATED_AT)]
        )
        older = GENERATED_AT - timedelta(days=1)
        kept = client.upload_flow_definitions(
            [FlowDefinitionDocument(flow_type, second, older)]
        )
        forced = client.upload_flow_definitions(
            [FlowDefinitionDocument(flow_type, second, older)], force=True
        )
    assert [result.outcome for result in created + unchanged + kept + forced] == [
        FlowDefinitionUploadOutcome.CREATED,
        FlowDefinitionUploadOutcome.UNCHANGED,
        FlowDefinitionUploadOutcome.KEPT_EXISTING,
        FlowDefinitionUploadOutcome.REPLACED,
    ]
    assert kept[0].stored_generated_at == GENERATED_AT
    assert forced[0].stored_generated_at == older
    assert get_flow_definitions([flow_type])[flow_type].content == second


def unique_flow_type(prefix: str) -> str:
    return f"{prefix}{uuid4().hex}"


def flow_definition_json(
    flow_type: str, source_path: str, generated_at: str | None = None
) -> bytes:
    document: dict[str, object] = {
        "schemaVersion": "2.0",
        "valid": True,
        "source": {"language": "python", "path": source_path},
        "flow": {"name": flow_type, "startStepId": "step:Start"},
        "nodes": [],
        "edges": [],
    }
    if generated_at is not None:
        document["generatedAt"] = generated_at
    return json.dumps(document, indent=2).encode()


def get_flow_definitions(flow_types: list[str]) -> dict[str, pb.FlowDefinition]:
    with grpc.insecure_channel(SERVER_ADDRESS) as channel:
        service = dex_pb2_grpc.FlowServiceStub(channel)  # type: ignore[no-untyped-call]
        response: pb.GetFlowDefinitionsResponse = service.GetFlowDefinitions(
            pb.GetFlowDefinitionsRequest(flow_types=flow_types), timeout=10
        )
    return {
        definition.info.flow_type: definition for definition in response.definitions
    }


class running_worker:
    def __init__(
        self, registry: Registry, blob_cache: BlobCache, options: WorkerOptions
    ) -> None:
        self._worker = Worker(registry, blob_cache, options)
        self._failure: BaseException | None = None
        self._thread = threading.Thread(
            target=self._run, name="dex-flow-definition-worker"
        )

    def __enter__(self) -> Worker:
        self._thread.start()
        self._worker.wait_pull_registered(timeout=30)
        return self._worker

    def __exit__(self, *exception_info: object) -> None:
        self._worker.close()
        self._thread.join(timeout=10)
        if self._thread.is_alive():
            raise RuntimeError("flow definition test Worker did not stop")
        if self._failure is not None:
            raise RuntimeError("flow definition test Worker failed") from self._failure

    def _run(self) -> None:
        try:
            self._worker.start()
        except BaseException as failure:
            self._failure = failure
