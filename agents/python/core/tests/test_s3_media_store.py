# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""S3MediaStore against a private s3local server, and a session that keeps images there.

The Agents CI has no s3local binary yet, so these tests run only when
``DEX_AGENTS_S3LOCAL`` names one, built with ``cargo build --release --bin
s3local`` in ``s3local/``.
"""

from __future__ import annotations

import asyncio
import base64
import hashlib
import json
import os
from collections.abc import AsyncIterator, Iterator
from dataclasses import dataclass, replace
from pathlib import Path
from typing import Any

import boto3  # type: ignore[import-untyped]
import pytest
import pytest_asyncio

from dex_agents import ImageValidationError, MediaStoreError, MessageRole, image_uri
from dex_agents.s3 import S3MediaStore
from dex_agents.testing import AgentTestStack, InvocationLog
from dex_agents.testing.processes import KillableProcess, find_free_port
from tests.conftest import CORE_DIR
from tests.image_agent import (
    IMAGE_LOADS_LOG_NAME,
    S3_BUCKET_ENVIRONMENT_VARIABLE,
    S3_ENDPOINT_ENVIRONMENT_VARIABLE,
    S3_PREFIX,
    S3LOCAL_ACCESS_KEY_ID,
    S3LOCAL_SECRET_ACCESS_KEY,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    build_image_agent_flow,
    screenshot_png,
)

S3LOCAL_ENVIRONMENT_VARIABLE = "DEX_AGENTS_S3LOCAL"
S3LOCAL_READY_LINE = "s3local is serving"
BUCKET = "agent-media"
# The Worker gets no AWS settings: the stores pass these loopback keys explicitly.
AWS_ENVIRONMENT_VARIABLES = (
    "AWS_ACCESS_KEY_ID",
    "AWS_SECRET_ACCESS_KEY",
    "AWS_SESSION_TOKEN",
    "AWS_PROFILE",
    "AWS_REGION",
    "AWS_DEFAULT_REGION",
)
IMAGES: dict[str, Any] = {
    case["name"]: case
    for case in json.loads(
        (Path(__file__).resolve().parents[3] / "conformance" / "images.json").read_text()
    )["images"]
}
PNG = base64.b64decode(IMAGES["png"]["base64"])

pytestmark = pytest.mark.skipif(
    not os.environ.get(S3LOCAL_ENVIRONMENT_VARIABLE),
    reason=f"needs an s3local binary; set {S3LOCAL_ENVIRONMENT_VARIABLE} to its path",
)


@dataclass
class S3Local:
    endpoint: str
    client: Any

    def store(self, prefix: str = S3_PREFIX) -> S3MediaStore:
        return S3MediaStore(
            BUCKET,
            prefix,
            endpoint_url=self.endpoint,
            region_name="us-east-1",
            force_path_style=True,
            access_key_id=S3LOCAL_ACCESS_KEY_ID,
            secret_access_key=S3LOCAL_SECRET_ACCESS_KEY,
        )

    def keys(self, prefix: str) -> list[str]:
        listing = self.client.list_objects_v2(Bucket=BUCKET, Prefix=prefix)
        return [entry["Key"] for entry in listing.get("Contents", [])]


@pytest.fixture
def without_aws_settings(monkeypatch: pytest.MonkeyPatch) -> Iterator[None]:
    for name in AWS_ENVIRONMENT_VARIABLES:
        monkeypatch.delenv(name, raising=False)
    yield


@pytest_asyncio.fixture
async def s3local(tmp_path: Path, without_aws_settings: None) -> AsyncIterator[S3Local]:
    port = find_free_port()
    process = KillableProcess("s3local", tmp_path / "s3local.log")
    await process.start(
        [
            os.environ[S3LOCAL_ENVIRONMENT_VARIABLE],
            "--listen-address",
            f"127.0.0.1:{port}",
            "--data-directory",
            str(tmp_path / "s3local-data"),
            "--durability",
            "process-crash-safe",
        ],
        S3LOCAL_READY_LINE,
        env={**os.environ, "RUST_LOG": "info"},
    )
    try:
        endpoint = f"http://127.0.0.1:{port}"
        client = boto3.session.Session().client(
            "s3",
            endpoint_url=endpoint,
            region_name="us-east-1",
            aws_access_key_id=S3LOCAL_ACCESS_KEY_ID,
            aws_secret_access_key=S3LOCAL_SECRET_ACCESS_KEY,
            config=boto3.session.Config(s3={"addressing_style": "path"}),
        )
        client.create_bucket(Bucket=BUCKET)
        yield S3Local(endpoint, client)
    finally:
        process.kill()


async def test_put_image_writes_one_object_per_image_with_its_content_type(
    s3local: S3Local,
) -> None:
    store = s3local.store()

    first = await store.put_image(PNG)
    second = await store.put_image(PNG)

    assert first == second
    assert first.uri == f"s3://{BUCKET}/{S3_PREFIX}/sha256/{IMAGES['png']['sha256']}"
    assert s3local.keys(S3_PREFIX) == [store.object_key(first.sha256)]
    head = s3local.client.head_object(Bucket=BUCKET, Key=store.object_key(first.sha256))
    assert head["ContentType"] == "image/png"
    assert await store.get_image(first) == PNG
    with pytest.raises(ImageValidationError, match="the limit is 3750000 bytes"):
        await store.put_image(b"\x89PNG\r\n\x1a\n" + bytes(3_750_000))
    with pytest.raises(ImageValidationError, match="not PNG, JPEG, GIF, or WebP"):
        await store.put_image(b"GIF00a")
    assert len(s3local.keys("")) == 1


async def test_concurrent_and_repeated_uploads_leave_one_object_version(s3local: S3Local) -> None:
    s3local.client.put_bucket_versioning(
        Bucket=BUCKET, VersioningConfiguration={"Status": "Enabled"}
    )
    store = s3local.store()
    other_store = s3local.store()

    references = await asyncio.gather(
        store.put_image(PNG), other_store.put_image(PNG), store.put_image(PNG)
    )

    assert len({reference.uri for reference in references}) == 1
    versions = s3local.client.list_object_versions(Bucket=BUCKET, Prefix=S3_PREFIX)
    assert [version["Key"] for version in versions.get("Versions", [])] == [
        store.object_key(references[0].sha256)
    ]


async def test_get_image_refuses_an_oversized_object_without_reading_it_all(
    s3local: S3Local,
) -> None:
    store = s3local.store()
    reference = await store.put_image(PNG)
    s3local.client.put_object(
        Bucket=BUCKET, Key=store.object_key(reference.sha256), Body=PNG + bytes(4_000_000)
    )

    with pytest.raises(MediaStoreError, match="does not match its sha256 and size"):
        await store.get_image(reference)


async def test_explicit_credentials_come_in_pairs_and_stay_out_of_the_repr(
    s3local: S3Local,
) -> None:
    secret = "never-shown-secret"
    store = S3MediaStore(
        BUCKET,
        S3_PREFIX,
        endpoint_url=s3local.endpoint,
        region_name="us-east-1",
        force_path_style=True,
        access_key_id="s3local",
        secret_access_key=secret,
    )

    assert secret not in repr(store)
    with pytest.raises(ValueError, match="access_key_id and secret_access_key together"):
        S3MediaStore(BUCKET, access_key_id="s3local")
    with pytest.raises(ValueError, match="session_token only with them"):
        S3MediaStore(BUCKET, session_token="token")
    # s3local checks no signatures, so this shows the explicit keys replace the missing AWS chain.
    assert (await store.put_image(PNG)).sha256 == IMAGES["png"]["sha256"]


async def test_get_image_reads_only_its_own_prefix_and_exact_bytes(s3local: S3Local) -> None:
    store = s3local.store()
    reference = await store.put_image(PNG)
    other_prefix = s3local.store("other")
    missing = replace(
        reference,
        uri=image_uri(store.store_uri, IMAGES["gif"]["sha256"]),
        sha256=IMAGES["gif"]["sha256"],
        mime_type="image/gif",
        size_bytes=IMAGES["gif"]["size_bytes"],
    )
    s3local.client.put_object(
        Bucket=BUCKET, Key=store.object_key(IMAGES["jpeg"]["sha256"]), Body=b"not the jpeg"
    )
    tampered = replace(
        reference,
        uri=image_uri(store.store_uri, IMAGES["jpeg"]["sha256"]),
        sha256=IMAGES["jpeg"]["sha256"],
        mime_type="image/jpeg",
        size_bytes=IMAGES["jpeg"]["size_bytes"],
    )

    with pytest.raises(MediaStoreError, match=f"is not in the media store s3://{BUCKET}/other"):
        await other_prefix.get_image(reference)
    with pytest.raises(MediaStoreError, match="is not in the media store"):
        await store.get_image(missing)
    with pytest.raises(MediaStoreError, match="does not match its sha256 and size"):
        await store.get_image(tampered)


async def test_a_session_keeps_user_and_tool_images_in_s3local_and_replays_them(
    tmp_path: Path, s3local: S3Local
) -> None:
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    worker_env = {
        STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir),
        S3_ENDPOINT_ENVIRONMENT_VARIABLE: s3local.endpoint,
        S3_BUCKET_ENVIRONMENT_VARIABLE: BUCKET,
    }
    flow = build_image_agent_flow(state_dir)
    async with AgentTestStack(
        tmp_path, [flow], "tests.image_worker", worker_cwd=CORE_DIR, worker_env=worker_env
    ) as stack:
        await stack.start_worker()
        sessions = stack.sessions(flow)
        store = s3local.store()
        png = await store.put_image(PNG)
        session_id = "session-s3local"
        await sessions.start_session(session_id)

        assert await sessions.send_user_message(session_id, "describe", images=[png])
        assert await sessions.send_user_message(session_id, "screenshot")
        assert await sessions.send_user_message(session_id, "describe")
        snapshot = await _wait_for_turns(sessions, session_id, 3)

    screenshot_sha256 = hashlib.sha256(screenshot_png()).hexdigest()
    assert snapshot.messages[0].images == [png]
    tool_messages = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [[image.sha256 for image in message.images] for message in tool_messages] == [
        [screenshot_sha256]
    ]
    assert snapshot.messages[-1].text == "saw 2 images"
    assert sorted(s3local.keys(S3_PREFIX)) == sorted(
        [store.object_key(png.sha256), store.object_key(screenshot_sha256)]
    )
    loads = InvocationLog(state_dir / IMAGE_LOADS_LOG_NAME).names()
    assert loads[-1] == (
        f"turn-3:1:{png.sha256}:{png.size_bytes},{screenshot_sha256}:{len(screenshot_png())}"
    )


async def _wait_for_turns(sessions: Any, session_id: str, turn_count: int) -> Any:
    from dex_agents.testing import wait_for_agent_snapshot

    return await wait_for_agent_snapshot(
        sessions,
        session_id,
        lambda snapshot: snapshot
        if snapshot.state is not None
        and snapshot.state.turn_count >= turn_count
        and snapshot.state.active_turn_id is None
        else None,
        description=f"{turn_count} finished turn(s)",
    )
