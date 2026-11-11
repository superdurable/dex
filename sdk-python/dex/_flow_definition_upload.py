# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import hashlib
import json
import logging
import os
from collections.abc import Collection, Iterator, Sequence
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path, PurePosixPath, PureWindowsPath
from typing import Any, Awaitable, Callable

import grpc
from google.protobuf.timestamp_pb2 import Timestamp

from dex.dexpb import dex_pb2 as pb
from dex.flow_definition import (
    FlowDefinitionDocument,
    FlowDefinitionUploadOutcome,
    FlowDefinitionUploadResult,
)

_LOGGER = logging.getLogger(__name__)

MAX_FLOW_DEFINITIONS_PER_UPLOAD = 256
MAX_FLOW_DEFINITION_UPLOAD_CONTENT_BYTES = 16 * 1024 * 1024
_JSON_WHITESPACE = " \t\n\r"
_JSON_DECODER = json.JSONDecoder()


@dataclass(frozen=True)
class PreparedFlowDefinition:
    flow_type: str
    content: bytes
    generated_at: datetime
    origin: str


def prepare_flow_definitions(
    paths: Sequence[str | os.PathLike[str]],
    documents: Sequence[FlowDefinitionDocument],
) -> tuple[PreparedFlowDefinition, ...]:
    """Load, validate, and merge the configured Flow definitions.

    Returns:
        One prepared definition for each Flow type, files first in sorted path
        order, then in-memory documents in the given order. Each content has its
        absolute ``source.path`` rewritten.

    Raises:
        ValueError: A file cannot be read or parsed, a document is invalid, or
            two definitions describe the same Flow type.
    """
    prepared: list[PreparedFlowDefinition] = []
    for file_path in _flow_definition_files(paths):
        prepared.append(_load_flow_definition_file(file_path))
    for document in documents:
        prepared.append(_prepare_document(document))
    origins_by_flow_type: dict[str, str] = {}
    for definition in prepared:
        existing_origin = origins_by_flow_type.get(definition.flow_type)
        if existing_origin is not None:
            raise ValueError(
                f"Flow definition for Flow type {definition.flow_type!r} is "
                f"configured twice: {existing_origin} and {definition.origin}"
            )
        origins_by_flow_type[definition.flow_type] = definition.origin
    return tuple(prepared)


def select_registered_flow_definitions(
    definitions: Sequence[PreparedFlowDefinition],
    registered_flow_types: Collection[str],
) -> list[PreparedFlowDefinition]:
    selected: list[PreparedFlowDefinition] = []
    for definition in definitions:
        if definition.flow_type in registered_flow_types:
            selected.append(definition)
        else:
            _LOGGER.info(
                "skipping Flow definition upload because this Worker does not "
                "register the Flow type: flow_type=%s origin=%s",
                definition.flow_type,
                definition.origin,
            )
    return selected


def upload_requests(
    definitions: Sequence[PreparedFlowDefinition],
    uploader: str,
    is_forced: bool,
) -> Iterator[tuple[pb.UploadFlowDefinitionsRequest, Sequence[PreparedFlowDefinition]]]:
    for batch in flow_definition_batches(definitions):
        request = pb.UploadFlowDefinitionsRequest(uploader=uploader, force=is_forced)
        for definition in batch:
            generated_at = Timestamp()
            generated_at.FromDatetime(definition.generated_at)
            request.definitions.append(
                pb.FlowDefinitionUpload(
                    flow_type=definition.flow_type,
                    content=definition.content,
                    generated_at=generated_at,
                )
            )
        yield request, batch


def flow_definition_batches(
    definitions: Sequence[PreparedFlowDefinition],
) -> list[Sequence[PreparedFlowDefinition]]:
    """Split definitions into upload batches in their given order.

    Returns:
        Batches of at most 256 definitions whose content totals at most 16 MiB.
        A larger single definition forms its own batch.
    """
    batches: list[Sequence[PreparedFlowDefinition]] = []
    batch_start = 0
    batch_bytes = 0
    for index, definition in enumerate(definitions):
        content_bytes = len(definition.content)
        is_batch_full = (
            index - batch_start == MAX_FLOW_DEFINITIONS_PER_UPLOAD
            or batch_bytes + content_bytes > MAX_FLOW_DEFINITION_UPLOAD_CONTENT_BYTES
        )
        if index > batch_start and is_batch_full:
            batches.append(definitions[batch_start:index])
            batch_start = index
            batch_bytes = 0
        batch_bytes += content_bytes
    if batch_start < len(definitions):
        batches.append(definitions[batch_start:])
    return batches


def upload_results_from_response(
    response: pb.UploadFlowDefinitionsResponse,
) -> list[FlowDefinitionUploadResult]:
    return [
        FlowDefinitionUploadResult(
            flow_type=result.flow_type,
            outcome=_upload_outcome(result.outcome),
            stored_digest=result.stored_digest,
            stored_generated_at=result.stored_generated_at.ToDatetime(
                tzinfo=timezone.utc
            ),
        )
        for result in response.results
    ]


def upload_worker_flow_definitions(
    upload: Callable[
        [pb.UploadFlowDefinitionsRequest], pb.UploadFlowDefinitionsResponse
    ],
    definitions: Sequence[PreparedFlowDefinition],
    registered_flow_types: Collection[str],
    uploader: str,
) -> None:
    """Upload a Worker's Flow definitions, logging every failure instead of raising."""
    selected = select_registered_flow_definitions(definitions, registered_flow_types)
    for request, batch in upload_requests(selected, uploader, False):
        try:
            response = upload(request)
            _log_worker_upload_results(batch, response)
        except Exception as failure:
            _log_worker_upload_failure(failure)
            return


async def upload_worker_flow_definitions_async(
    upload: Callable[
        [pb.UploadFlowDefinitionsRequest], Awaitable[pb.UploadFlowDefinitionsResponse]
    ],
    definitions: Sequence[PreparedFlowDefinition],
    registered_flow_types: Collection[str],
    uploader: str,
) -> None:
    """Upload a Worker's Flow definitions, logging every failure instead of raising."""
    selected = select_registered_flow_definitions(definitions, registered_flow_types)
    for request, batch in upload_requests(selected, uploader, False):
        try:
            response = await upload(request)
            _log_worker_upload_results(batch, response)
        except Exception as failure:
            _log_worker_upload_failure(failure)
            return


def flow_definition_uploader(sdk_version: str) -> str:
    return f"sdk-python/{sdk_version}"


def _log_worker_upload_failure(failure: Exception) -> None:
    if (
        isinstance(failure, grpc.RpcError)
        and failure.code() == grpc.StatusCode.UNIMPLEMENTED
    ):
        _LOGGER.warning(
            "Dex Server does not support Flow definition upload; upgrade the "
            "Server to store Flow definitions. The Worker continues starting: %s",
            failure,
        )
        return
    _LOGGER.warning(
        "Flow definition upload failed; the Worker continues starting: %s",
        failure,
    )


def _log_worker_upload_results(
    batch: Sequence[PreparedFlowDefinition],
    response: pb.UploadFlowDefinitionsResponse,
) -> None:
    definitions_by_flow_type = {
        definition.flow_type: definition for definition in batch
    }
    for result in upload_results_from_response(response):
        definition = definitions_by_flow_type.get(result.flow_type)
        origin = definition.origin if definition is not None else "unknown"
        if result.outcome is FlowDefinitionUploadOutcome.UNCHANGED:
            _LOGGER.debug(
                "Flow definition is unchanged: flow_type=%s origin=%s digest=%s",
                result.flow_type,
                origin,
                result.stored_digest,
            )
        elif result.outcome is FlowDefinitionUploadOutcome.UNSPECIFIED:
            _LOGGER.warning(
                "Dex Server returned an unknown Flow definition upload outcome: "
                "flow_type=%s origin=%s stored_digest=%s",
                result.flow_type,
                origin,
                result.stored_digest,
            )
        elif result.outcome is FlowDefinitionUploadOutcome.KEPT_EXISTING:
            uploaded_digest = (
                hashlib.sha256(definition.content).hexdigest()
                if definition is not None
                else "unknown"
            )
            uploaded_generated_at = (
                definition.generated_at.isoformat()
                if definition is not None
                else "unknown"
            )
            _LOGGER.warning(
                "Dex Server kept a different Flow definition generated at the same "
                "time or later; upload with force to replace it: flow_type=%s "
                "origin=%s uploaded_digest=%s uploaded_generated_at=%s "
                "stored_digest=%s stored_generated_at=%s",
                result.flow_type,
                origin,
                uploaded_digest,
                uploaded_generated_at,
                result.stored_digest,
                result.stored_generated_at.isoformat(),
            )
        else:
            _LOGGER.info(
                "Flow definition %s: flow_type=%s origin=%s digest=%s",
                result.outcome.value,
                result.flow_type,
                origin,
                result.stored_digest,
            )


def _upload_outcome(outcome: int) -> FlowDefinitionUploadOutcome:
    if outcome == pb.FLOW_DEFINITION_UPLOAD_OUTCOME_CREATED:
        return FlowDefinitionUploadOutcome.CREATED
    if outcome == pb.FLOW_DEFINITION_UPLOAD_OUTCOME_REPLACED:
        return FlowDefinitionUploadOutcome.REPLACED
    if outcome == pb.FLOW_DEFINITION_UPLOAD_OUTCOME_UNCHANGED:
        return FlowDefinitionUploadOutcome.UNCHANGED
    if outcome == pb.FLOW_DEFINITION_UPLOAD_OUTCOME_KEPT_EXISTING:
        return FlowDefinitionUploadOutcome.KEPT_EXISTING
    return FlowDefinitionUploadOutcome.UNSPECIFIED


def _flow_definition_files(paths: Sequence[str | os.PathLike[str]]) -> list[Path]:
    files: list[Path] = []
    for configured_path in paths:
        path = Path(configured_path)
        if path.is_dir():
            files.extend(
                sorted(
                    candidate
                    for candidate in path.rglob("*.json")
                    if candidate.is_file()
                )
            )
        elif path.is_file():
            files.append(path)
        else:
            raise ValueError(f"Flow definition path {str(path)!r} does not exist")
    return files


def _load_flow_definition_file(file_path: Path) -> PreparedFlowDefinition:
    origin = f"file {str(file_path)!r}"
    try:
        content = file_path.read_bytes()
        modified_at = datetime.fromtimestamp(file_path.stat().st_mtime, timezone.utc)
    except OSError as failure:
        raise ValueError(
            f"cannot read Flow definition {origin}: {failure}"
        ) from failure
    document = _parse_flow_definition(content, origin)
    flow_type = _flow_name(document, origin)
    generated_at = modified_at
    if "generatedAt" in document:
        generated_at = _parse_generated_at(document["generatedAt"], origin)
    return PreparedFlowDefinition(
        flow_type=flow_type,
        content=_rewrite_source_path(content, document),
        generated_at=generated_at,
        origin=origin,
    )


def _prepare_document(document: FlowDefinitionDocument) -> PreparedFlowDefinition:
    origin = f"in-memory document for Flow type {document.flow_type!r}"
    content = (
        document.content.encode("utf-8")
        if isinstance(document.content, str)
        else bytes(document.content)
    )
    parsed = _parse_flow_definition(content, origin)
    flow_name = _flow_name(parsed, origin)
    if flow_name != document.flow_type:
        raise ValueError(
            f"Flow definition {origin} has flow.name {flow_name!r}; it must equal the Flow type"
        )
    return PreparedFlowDefinition(
        flow_type=document.flow_type,
        content=_rewrite_source_path(content, parsed),
        generated_at=document.generated_at,
        origin=origin,
    )


def _parse_flow_definition(content: bytes, origin: str) -> dict[str, Any]:
    try:
        document = json.loads(content.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as failure:
        raise ValueError(
            f"Flow definition {origin} is not UTF-8 JSON: {failure}"
        ) from failure
    if not isinstance(document, dict):
        raise ValueError(f"Flow definition {origin} is not a JSON object")
    return document


def _flow_name(document: dict[str, Any], origin: str) -> str:
    flow = document.get("flow")
    name = flow.get("name") if isinstance(flow, dict) else None
    if not isinstance(name, str) or not name:
        raise ValueError(f"Flow definition {origin} has no flow.name string")
    return name


def _parse_generated_at(value: object, origin: str) -> datetime:
    if not isinstance(value, str):
        raise ValueError(
            f"Flow definition {origin} has a generatedAt that is not a string"
        )
    try:
        generated_at = datetime.fromisoformat(value)
    except ValueError as failure:
        raise ValueError(
            f"Flow definition {origin} has generatedAt {value!r}, which is not RFC 3339"
        ) from failure
    if generated_at.tzinfo is None:
        raise ValueError(
            f"Flow definition {origin} has generatedAt {value!r} without a time zone offset"
        )
    return generated_at


def _rewrite_source_path(content: bytes, document: dict[str, Any]) -> bytes:
    source = document.get("source")
    if not isinstance(source, dict) or not isinstance(source.get("path"), str):
        return content
    text = content.decode("utf-8")
    replacements: list[tuple[int, int, str]] = []
    for key, source_start, _ in _object_members(text, _skip_whitespace(text, 0)):
        if key != "source" or text[source_start] != "{":
            continue
        for member_key, value_start, value_end in _object_members(text, source_start):
            if member_key != "path" or text[value_start] != '"':
                continue
            original_path, _ = _JSON_DECODER.raw_decode(text, value_start)
            shared_path = _shareable_source_path(original_path)
            if shared_path != original_path:
                replacements.append(
                    (
                        value_start,
                        value_end,
                        json.dumps(shared_path, ensure_ascii=False),
                    )
                )
    if not replacements:
        return content
    for value_start, value_end, encoded_path in reversed(replacements):
        text = text[:value_start] + encoded_path + text[value_end:]
    return text.encode("utf-8")


def _shareable_source_path(path: str) -> str:
    if PurePosixPath(path).is_absolute():
        absolute_path = Path(path)
        try:
            return absolute_path.relative_to(Path.cwd()).as_posix()
        except ValueError:
            return absolute_path.name
    windows_path = PureWindowsPath(path)
    if windows_path.is_absolute():
        return windows_path.name
    return path


def _object_members(text: str, start: int) -> Iterator[tuple[str, int, int]]:
    """Yield each member of the JSON object at ``start``.

    Returns:
        An iterator of (key, value start, value end) offsets into ``text``. The
        caller has already validated that ``text`` is JSON.
    """
    position = _skip_whitespace(text, start + 1)
    if text[position] == "}":
        return
    while True:
        key, position = _JSON_DECODER.raw_decode(text, position)
        position = _skip_whitespace(text, position)
        value_start = _skip_whitespace(text, position + 1)
        _, value_end = _JSON_DECODER.raw_decode(text, value_start)
        yield key, value_start, value_end
        position = _skip_whitespace(text, value_end)
        if text[position] == "}":
            return
        position = _skip_whitespace(text, position + 1)


def _skip_whitespace(text: str, position: int) -> int:
    while text[position] in _JSON_WHITESPACE:
        position += 1
    return position
