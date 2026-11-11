# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The shared image cases in agents/conformance/images.json, and the in-memory store."""

from __future__ import annotations

import base64
import json
from dataclasses import replace
from pathlib import Path
from typing import Any

import pytest
from dex import CodecRegistry
from dex.codec import Value, WireKind
from mcp_types import CallToolResult

from dex_agents import (
    MAX_IMAGE_BYTES,
    MAX_IMAGE_URI_LENGTH,
    MAX_IMAGES_PER_MESSAGE,
    SUPPORTED_IMAGE_MIME_TYPES,
    AgentImageReference,
    AgentMessage,
    AgentToolCall,
    AgentTurnContext,
    ImageValidationError,
    InMemoryMediaStore,
    MediaStoreError,
    MessageRole,
    ToolCallStatus,
    ToolEffect,
    build_image_reference,
    collect_turn_images,
    detect_image_mime_type,
    image_uri,
    load_images,
    require_short_image_uris,
    validate_image_reference,
    validate_image_references,
)
from dex_agents.mcp._image_content import store_tool_result_images
from dex_agents.mcp._tool_source import (
    _outcome_from_tool_result,
    _ToolCallFailed,
    _ToolCallOutcomeUnknown,
)
from dex_agents.runner import ToolExecutionError
from dex_agents.s3 import S3MediaStore

IMAGES_FILE = Path(__file__).resolve().parents[3] / "conformance" / "images.json"
SUITE: dict[str, Any] = json.loads(IMAGES_FILE.read_text(encoding="utf-8"))
REFERENCE_CODEC = CodecRegistry().resolve(AgentImageReference)
IMAGES_BY_NAME: dict[str, dict[str, Any]] = {case["name"]: case for case in SUITE["images"]}


def decode_reference(wire: str) -> AgentImageReference:
    reference: AgentImageReference = REFERENCE_CODEC.decode(Value(WireKind.JSON, wire))
    return reference


def canonical_reference(reference: AgentImageReference) -> str:
    encoded = json.loads(str(REFERENCE_CODEC.encode(reference).data))
    return json.dumps(encoded, sort_keys=True, separators=(",", ":"))


def image_bytes(case: dict[str, Any]) -> bytes:
    return base64.b64decode(case["base64"])


def test_the_limits_match_the_core_constants() -> None:
    limits = SUITE["limits"]
    assert limits["max_image_bytes"] == MAX_IMAGE_BYTES
    assert limits["max_images_per_message"] == MAX_IMAGES_PER_MESSAGE
    assert limits["max_image_uri_length"] == MAX_IMAGE_URI_LENGTH
    assert limits["supported_mime_types"] == list(SUPPORTED_IMAGE_MIME_TYPES)


@pytest.mark.parametrize("case", SUITE["images"], ids=lambda case: case["name"])
def test_each_image_gets_its_type_hash_size_and_canonical_reference(case: dict[str, Any]) -> None:
    data = image_bytes(case)
    reference = build_image_reference(data, "memory://conformance")

    assert detect_image_mime_type(data) == case["mime_type"]
    assert (reference.mime_type, reference.sha256, reference.size_bytes) == (
        case["mime_type"],
        case["sha256"],
        case["size_bytes"],
    )
    assert canonical_reference(reference) == case["reference"]


@pytest.mark.parametrize("case", SUITE["padded_images"], ids=lambda case: case["name"])
def test_padded_images_are_checked_for_size_then_structure(case: dict[str, Any]) -> None:
    prefix = base64.b64decode(case["prefix_base64"])
    data = prefix + bytes(case["size_bytes"] - len(prefix))
    if case["error"] is None:
        assert build_image_reference(data, "memory://conformance").mime_type == case["mime_type"]
    else:
        with pytest.raises(ImageValidationError) as raised:
            build_image_reference(data, "memory://conformance")
        assert str(raised.value) == case["error"]


@pytest.mark.parametrize("case", SUITE["rejected_images"], ids=lambda case: case["name"])
def test_bytes_that_are_not_a_supported_image_are_refused(case: dict[str, Any]) -> None:
    with pytest.raises(ImageValidationError) as raised:
        build_image_reference(image_bytes(case), "memory://conformance")
    assert str(raised.value) == case["error"]


@pytest.mark.parametrize("case", SUITE["store_locations"]["s3"], ids=lambda case: case["name"])
def test_an_s3_store_names_objects_by_content(case: dict[str, Any]) -> None:
    sha256 = SUITE["store_locations"]["sha256"]
    store = S3MediaStore(case["bucket"], case["prefix"], region_name="us-east-1")

    assert store.store_uri == case["store_uri"]
    assert store.object_key(sha256) == case["object_key"]
    assert image_uri(store.store_uri, sha256) == case["image_uri"]
    assert case["image_uri"] == f"s3://{case['bucket']}/{case['object_key']}"


@pytest.mark.parametrize(
    "case", SUITE["store_locations"]["s3_store_uris"], ids=lambda case: case["store_uri"]
)
def test_an_s3_store_uri_names_a_bucket_and_prefix(case: dict[str, Any]) -> None:
    store = S3MediaStore.from_store_uri(case["store_uri"], region_name="us-east-1")
    expected = S3MediaStore(case["bucket"], case["prefix"], region_name="us-east-1")
    assert store.store_uri == expected.store_uri


@pytest.mark.parametrize("case", SUITE["store_locations"]["memory"], ids=lambda case: case["name"])
def test_an_in_memory_store_names_images_under_its_name(case: dict[str, Any]) -> None:
    store = InMemoryMediaStore(case["name"])
    assert store.store_uri == case["store_uri"]
    assert image_uri(store.store_uri, SUITE["store_locations"]["sha256"]) == case["image_uri"]


@pytest.mark.parametrize("case", SUITE["verify_cases"], ids=lambda case: case["name"])
async def test_a_reference_whose_type_was_changed_fails_on_read(case: dict[str, Any]) -> None:
    store = InMemoryMediaStore("conformance")
    reference = await store.put_image(image_bytes(IMAGES_BY_NAME[case["image"]]))
    relabeled = replace(reference, mime_type=case["reference_mime_type"])

    validate_image_reference(relabeled)
    with pytest.raises(MediaStoreError) as raised:
        await store.get_image(relabeled)
    assert str(raised.value) == case["error"].replace("{uri}", reference.uri)


@pytest.mark.parametrize(
    "case", SUITE["store_locations"]["too_long"], ids=lambda case: case["name"]
)
def test_a_store_location_too_long_for_its_image_uris_is_refused(case: dict[str, Any]) -> None:
    store_uri = case["store_uri"]

    with pytest.raises(ValueError) as raised:
        require_short_image_uris(store_uri)
    assert str(raised.value) == case["store_error"]
    with pytest.raises(ValueError, match="makes image URIs longer than 1024 bytes as UTF-8"):
        InMemoryMediaStore(store_uri.removeprefix("memory://"))
    with pytest.raises(ValueError, match="makes image URIs longer than 1024 bytes as UTF-8"):
        S3MediaStore("agent-media", store_uri.removeprefix("memory://"), region_name="us-east-1")
    with pytest.raises(ImageValidationError) as built:
        build_image_reference(image_bytes(IMAGES_BY_NAME["png"]), store_uri)
    assert str(built.value) == case["build_error"]


@pytest.mark.parametrize("case", SUITE["references"]["valid"], ids=lambda case: case["name"])
def test_valid_references_pass(case: dict[str, Any]) -> None:
    validate_image_reference(decode_reference(case["wire"]))


@pytest.mark.parametrize("case", SUITE["references"]["invalid"], ids=lambda case: case["name"])
def test_invalid_references_fail_with_the_shared_text(case: dict[str, Any]) -> None:
    with pytest.raises(ImageValidationError) as raised:
        validate_image_reference(decode_reference(case["wire"]))
    assert str(raised.value) == case["error"]


@pytest.mark.parametrize("case", SUITE["reference_lists"], ids=lambda case: case["name"])
def test_reference_lists_are_bounded_and_name_the_bad_index(case: dict[str, Any]) -> None:
    references = [decode_reference(wire) for wire in case["wires"]] * case["repeat"]
    if case["error"] is None:
        validate_image_references(references)
    else:
        with pytest.raises(ImageValidationError) as raised:
            validate_image_references(references)
        assert str(raised.value) == case["error"]


async def test_the_in_memory_store_keeps_one_object_per_image_and_reads_it_back() -> None:
    store = InMemoryMediaStore("conformance")
    png = image_bytes(SUITE["images"][0])

    first = await store.put_image(png)
    second = await store.put_image(png)

    assert first == second
    assert store.image_count == 1
    assert await store.get_image(first) == png
    with pytest.raises(ImageValidationError, match="the limit is 3750000 bytes"):
        await store.put_image(b"\x89PNG\r\n\x1a\n" + bytes(MAX_IMAGE_BYTES))
    with pytest.raises(ImageValidationError, match="not PNG, JPEG, GIF, or WebP"):
        await store.put_image(b"<svg/>")
    assert store.image_count == 1


async def test_the_in_memory_store_reads_only_its_own_images_with_their_exact_bytes() -> None:
    store = InMemoryMediaStore("conformance")
    reference = await store.put_image(image_bytes(SUITE["images"][0]))
    other_store_reference = replace(
        reference, uri=image_uri("memory://elsewhere", reference.sha256)
    )
    missing = build_image_reference(image_bytes(SUITE["images"][1]), store.store_uri)

    with pytest.raises(MediaStoreError, match="is not in the media store memory://conformance"):
        await store.get_image(other_store_reference)
    with pytest.raises(MediaStoreError, match="is not in the media store"):
        await store.get_image(missing)
    with pytest.raises(ImageValidationError, match="mime_type"):
        await store.get_image(replace(reference, mime_type="image/bmp"))


async def test_load_images_reads_each_distinct_image_once() -> None:
    reads: list[str] = []

    class CountingStore(InMemoryMediaStore):
        async def get_image(self, reference: AgentImageReference) -> bytes:
            reads.append(reference.sha256)
            return await super().get_image(reference)

    store = CountingStore()
    png = await store.put_image(image_bytes(SUITE["images"][0]))
    gif = await store.put_image(image_bytes(SUITE["images"][2]))

    loaded = await load_images(store, [png, gif, png])

    assert sorted(reads) == sorted([png.sha256, gif.sha256])
    assert loaded == {
        png.sha256: image_bytes(SUITE["images"][0]),
        gif.sha256: image_bytes(SUITE["images"][2]),
    }


def test_collect_turn_images_takes_messages_then_records_each_image_once() -> None:
    store_uri = "memory://conformance"
    png, jpeg, gif, webp = (
        build_image_reference(image_bytes(case), store_uri) for case in SUITE["images"]
    )

    def message(sequence: int, images: list[AgentImageReference]) -> AgentMessage:
        return AgentMessage(
            sequence=sequence,
            turn_id="turn-1",
            role=MessageRole.USER,
            framework_item="{}",
            created_at="2026-11-10T00:00:00.000Z",
            images=images,
        )

    def record(tool_call_id: str, order: int, images: list[AgentImageReference]) -> AgentToolCall:
        return AgentToolCall(
            tool_call_id=tool_call_id,
            turn_id="turn-1",
            tool_name="take_screenshot",
            arguments="{}",
            effect=ToolEffect.READ,
            requires_approval=False,
            status=ToolCallStatus.SUCCEEDED,
            deferral_order=order,
            images=images,
        )

    turn = AgentTurnContext(
        flow_id="session-1",
        turn_id="turn-1",
        attempt=1,
        messages=[message(1, [png, jpeg]), message(2, [png])],
        framework_run_state="{}",
        tool_calls={"b": record("b", 1, [webp]), "a": record("a", 0, [gif, jpeg])},
        tool_policies={},
        default_tool_effect=ToolEffect.UNCLASSIFIED,
        events=None,  # type: ignore[arg-type]
        heartbeat=None,  # type: ignore[arg-type]
    )

    assert collect_turn_images(turn) == [png, jpeg, gif, webp]


@pytest.mark.parametrize("case", SUITE["mcp_image_content"]["cases"], ids=lambda case: case["name"])
async def test_mcp_image_content_becomes_references_with_the_shared_text(
    case: dict[str, Any],
) -> None:
    store = InMemoryMediaStore("mcp-conformance")
    result = CallToolResult.model_validate({"content": case["content"]})
    tool_name = SUITE["mcp_image_content"]["tool_name"]
    if "expected_error" in case:
        with pytest.raises(ToolExecutionError) as raised:
            await store_tool_result_images(result, store, tool_name)
        assert str(raised.value) == case["expected_error"]
        return
    converted = await store_tool_result_images(result, store, tool_name)
    assert converted.output == case["expected"]["output"]
    assert [
        {"sha256": image.sha256, "mime_type": image.mime_type, "size_bytes": image.size_bytes}
        for image in converted.images
    ] == case["expected"]["images"]
    known_bytes = {known["sha256"]: image_bytes(known) for known in SUITE["images"]}
    for image in converted.images:
        assert await store.get_image(image) == known_bytes[image.sha256]


@pytest.mark.parametrize(
    "case", SUITE["mcp_image_content"]["failure_cases"], ids=lambda case: case["name"]
)
def test_a_failed_mcp_result_with_images_records_no_image_bytes(case: dict[str, Any]) -> None:
    outcome = _outcome_from_tool_result(CallToolResult.model_validate(case["result"]))

    kinds = {_ToolCallFailed: "failed", _ToolCallOutcomeUnknown: "outcome_unknown"}
    assert isinstance(outcome, _ToolCallFailed | _ToolCallOutcomeUnknown)
    assert {"kind": kinds[type(outcome)], "message": outcome.message} == case["expected"]
