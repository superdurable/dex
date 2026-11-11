# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""S3MediaStore's conditional upload against a scripted S3 client: 412, 409, and fallback."""

from __future__ import annotations

import base64
import json
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import boto3  # type: ignore[import-untyped]
import pytest
from botocore.exceptions import ClientError  # type: ignore[import-untyped]

from dex_agents import MediaStoreError
from dex_agents.s3 import S3MediaStore

PNG_CASE: dict[str, Any] = next(
    case
    for case in json.loads(
        (Path(__file__).resolve().parents[3] / "conformance" / "images.json").read_text()
    )["images"]
    if case["name"] == "png"
)
PNG = base64.b64decode(PNG_CASE["base64"])


def s3_error(status: int, code: str) -> ClientError:
    return ClientError(
        {"Error": {"Code": code}, "ResponseMetadata": {"HTTPStatusCode": status}}, "S3"
    )


class ScriptedS3Client:
    """Answers each put_object with the next scripted error, or success once the script ends."""

    def __init__(self, put_errors: list[ClientError], stored_size: int | None) -> None:
        self.put_errors = put_errors
        self.stored_size = stored_size
        self.puts: list[dict[str, Any]] = []
        self.heads = 0

    def put_object(self, **parameters: Any) -> dict[str, Any]:
        self.puts.append(parameters)
        if self.put_errors:
            raise self.put_errors.pop(0)
        self.stored_size = len(parameters["Body"])
        return {}

    def head_object(self, **_: Any) -> dict[str, Any]:
        self.heads += 1
        if self.stored_size is None:
            raise s3_error(404, "404")
        return {"ContentLength": self.stored_size}


@pytest.fixture
def scripted_client(monkeypatch: pytest.MonkeyPatch) -> Iterator[list[ScriptedS3Client]]:
    clients: list[ScriptedS3Client] = []
    monkeypatch.setattr(
        boto3.session.Session, "client", lambda self, *arguments, **options: clients[0]
    )
    yield clients


def new_store() -> S3MediaStore:
    return S3MediaStore(
        "agent-media", "images", region_name="us-east-1", access_key_id="a", secret_access_key="b"
    )


async def test_an_existing_object_answers_412_and_needs_no_head(
    scripted_client: list[ScriptedS3Client],
) -> None:
    client = ScriptedS3Client([s3_error(412, "PreconditionFailed")], stored_size=len(PNG))
    scripted_client.append(client)

    reference = await new_store().put_image(PNG)

    assert reference.sha256 == PNG_CASE["sha256"]
    assert [put["IfNoneMatch"] for put in client.puts] == ["*"]
    assert client.heads == 0


async def test_a_409_conflict_retries_the_conditional_upload(
    scripted_client: list[ScriptedS3Client],
) -> None:
    client = ScriptedS3Client([s3_error(409, "ConditionalRequestConflict")], stored_size=None)
    scripted_client.append(client)

    reference = await new_store().put_image(PNG)

    assert reference.size_bytes == len(PNG)
    assert [put["IfNoneMatch"] for put in client.puts] == ["*", "*"]
    assert client.stored_size == len(PNG)


async def test_conflicts_on_every_attempt_with_the_object_missing_fail_the_upload(
    scripted_client: list[ScriptedS3Client],
) -> None:
    conflict = s3_error(409, "ConditionalRequestConflict")
    client = ScriptedS3Client([conflict, conflict, conflict], stored_size=None)
    scripted_client.append(client)

    with pytest.raises(MediaStoreError, match="was not stored: every upload conflicted"):
        await new_store().put_image(PNG)
    assert len(client.puts) == 3
    assert client.heads == 1


async def test_conflicts_on_every_attempt_succeed_when_the_object_is_there(
    scripted_client: list[ScriptedS3Client],
) -> None:
    conflict = s3_error(409, "ConditionalRequestConflict")
    client = ScriptedS3Client([conflict, conflict, conflict], stored_size=len(PNG))
    scripted_client.append(client)

    reference = await new_store().put_image(PNG)

    assert reference.sha256 == PNG_CASE["sha256"]
    assert (len(client.puts), client.heads) == (3, 1)


async def test_conflicts_with_an_object_of_another_size_fail_the_upload(
    scripted_client: list[ScriptedS3Client],
) -> None:
    conflict = s3_error(409, "ConditionalRequestConflict")
    client = ScriptedS3Client([conflict, conflict, conflict], stored_size=len(PNG) + 1)
    scripted_client.append(client)

    with pytest.raises(MediaStoreError, match="was not stored"):
        await new_store().put_image(PNG)


async def test_a_service_without_conditional_writes_falls_back_to_head_then_put(
    scripted_client: list[ScriptedS3Client],
) -> None:
    client = ScriptedS3Client([s3_error(501, "NotImplemented")], stored_size=None)
    scripted_client.append(client)
    store = new_store()

    await store.put_image(PNG)
    await store.put_image(PNG)

    assert ["IfNoneMatch" in put for put in client.puts] == [True, False]
    assert client.heads == 2
