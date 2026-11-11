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
import re
from collections.abc import Collection, Iterator, Sequence
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from time import monotonic
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
_SCHEMA_VERSION_PREFIX = "2."
_JSON_WHITESPACE = " \t\n\r"
_JSON_DECODER = json.JSONDecoder()
_RFC3339_PATTERN = re.compile(
    r"(\d{4})-(\d{2})-(\d{2})[Tt](\d{2}):(\d{2}):(\d{2})(?:\.(\d+))?"
    r"(?:([Zz])|([+-])(\d{2}):(\d{2}))"
)
_DRIVE_PATTERN = re.compile(r"[A-Za-z]:")
_ABSOLUTE_DRIVE_PATTERN = re.compile(r"[A-Za-z]:[\\/]")
_PATH_SEPARATOR_PATTERN = re.compile(r"[\\/]")
_JSON_SHORT_ESCAPES = {
    '"': '\\"',
    "\\": "\\\\",
    "\b": "\\b",
    "\f": "\\f",
    "\n": "\\n",
    "\r": "\\r",
    "\t": "\\t",
}


class _InvalidFlowDefinition(ValueError):
    """A file is not a Flow Definition Graph 2.x document."""


@dataclass(frozen=True)
class PreparedFlowDefinition:
    flow_type: str
    content: bytes
    generated_at: datetime
    origin: str


UploadCall = Callable[
    [pb.UploadFlowDefinitionsRequest, float], pb.UploadFlowDefinitionsResponse
]
AsyncUploadCall = Callable[
    [pb.UploadFlowDefinitionsRequest, float],
    Awaitable[pb.UploadFlowDefinitionsResponse],
]


def prepare_flow_definitions(
    paths: Sequence[str | os.PathLike[str]],
    documents: Sequence[FlowDefinitionDocument],
) -> tuple[PreparedFlowDefinition, ...]:
    """Load, validate, and merge the configured Flow definitions.

    Returns:
        One prepared definition for each Flow type, files first, then in-memory
        documents in the given order. Each content has its absolute
        ``source.path`` rewritten and each time is truncated to milliseconds.

    Raises:
        ValueError: A path is missing or unreadable, an explicit file or an
            in-memory document is invalid, a directory FDG has an invalid
            generatedAt, or two definitions describe the same Flow type.
    """
    working_directory = _working_directory()
    prepared: list[PreparedFlowDefinition] = []
    for configured_path in paths:
        prepared.extend(_load_configured_path(Path(configured_path), working_directory))
    for document in documents:
        prepared.append(_prepare_document(document, working_directory))
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
    upload: UploadCall,
    definitions: Sequence[PreparedFlowDefinition],
    registered_flow_types: Collection[str],
    uploader: str,
    timeout_seconds: float,
) -> None:
    """Upload a Worker's Flow definitions within one total deadline, logging every failure."""
    selected = select_registered_flow_definitions(definitions, registered_flow_types)
    deadline = monotonic() + timeout_seconds
    for request, batch in upload_requests(selected, uploader, False):
        remaining_seconds = deadline - monotonic()
        if remaining_seconds <= 0:
            _log_upload_deadline_exceeded()
            return
        try:
            response = upload(request, remaining_seconds)
            _log_worker_upload_results(batch, response)
        except Exception as failure:
            _log_worker_upload_failure(failure)
            return


async def upload_worker_flow_definitions_async(
    upload: AsyncUploadCall,
    definitions: Sequence[PreparedFlowDefinition],
    registered_flow_types: Collection[str],
    uploader: str,
    timeout_seconds: float,
) -> None:
    """Upload a Worker's Flow definitions within one total deadline, logging every failure."""
    selected = select_registered_flow_definitions(definitions, registered_flow_types)
    deadline = monotonic() + timeout_seconds
    for request, batch in upload_requests(selected, uploader, False):
        remaining_seconds = deadline - monotonic()
        if remaining_seconds <= 0:
            _log_upload_deadline_exceeded()
            return
        try:
            response = await upload(request, remaining_seconds)
            _log_worker_upload_results(batch, response)
        except Exception as failure:
            _log_worker_upload_failure(failure)
            return


def flow_definition_uploader(sdk_version: str) -> str:
    return f"sdk-python/{sdk_version}"


def shareable_source_path(
    source_path: str, working_directory: str | None
) -> str | None:
    """Return the shared form of an absolute ``source.path``.

    Returns:
        None when ``source_path`` is not absolute and stays unchanged. Otherwise
        the path relative to ``working_directory`` joined with "/", or the base
        name when it is outside the directory or the directory is unknown.
    """
    if not _is_absolute_source_path(source_path):
        return None
    path_segments = _lexical_path_segments(source_path)
    if working_directory is not None:
        directory_segments = _lexical_path_segments(working_directory)
        if (
            len(path_segments) > len(directory_segments)
            and path_segments[: len(directory_segments)] == directory_segments
        ):
            return "/".join(path_segments[len(directory_segments) :])
    return path_segments[-1]


def encode_json_string(value: str) -> str:
    """Encode a JSON string escaping only quotes, backslashes, and control characters.

    Returns:
        The quoted JSON string.
    """
    encoded = []
    for character in value:
        escape = _JSON_SHORT_ESCAPES.get(character)
        if escape is not None:
            encoded.append(escape)
        elif ord(character) < 0x20:
            encoded.append(f"\\u{ord(character):04x}")
        else:
            encoded.append(character)
    return '"' + "".join(encoded) + '"'


def parse_rfc3339_milliseconds(value: str) -> datetime:
    """Parse a strict RFC 3339 time and truncate it to whole milliseconds.

    Returns:
        An aware UTC datetime.

    Raises:
        ValueError: ``value`` is not a strict RFC 3339 date and time with an offset.
    """
    match = _RFC3339_PATTERN.fullmatch(value)
    if match is None:
        raise ValueError(f"{value!r} is not an RFC 3339 time")
    (
        year,
        month,
        day,
        hour,
        minute,
        second,
        fraction,
        _,
        sign,
        offset_hour,
        offset_minute,
    ) = match.groups()
    offset = timedelta(0)
    if sign is not None:
        if int(offset_hour) > 23 or int(offset_minute) > 59:
            raise ValueError(f"{value!r} has an offset outside -23:59..+23:59")
        offset = timedelta(hours=int(offset_hour), minutes=int(offset_minute))
        if sign == "-":
            offset = -offset
    milliseconds = int((fraction or "").ljust(3, "0")[:3])
    local_time = datetime(
        int(year),
        int(month),
        int(day),
        int(hour),
        int(minute),
        int(second),
        milliseconds * 1000,
        tzinfo=timezone(offset),
    )
    return local_time.astimezone(timezone.utc)


def truncate_to_milliseconds(value: datetime) -> datetime:
    """Return ``value`` as an aware UTC datetime truncated to whole milliseconds.

    Returns:
        The truncated time; a naive ``value`` is treated as UTC.
    """
    aware = value if value.tzinfo is not None else value.replace(tzinfo=timezone.utc)
    utc_time = aware.astimezone(timezone.utc)
    return utc_time.replace(microsecond=utc_time.microsecond // 1000 * 1000)


def _log_upload_deadline_exceeded() -> None:
    _LOGGER.warning(
        "Flow definition upload ran out of time before every batch was sent; "
        "the Worker continues starting"
    )


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
    for raw_result, result in zip(
        response.results, upload_results_from_response(response)
    ):
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
                "unknown upload outcome %d: flow_type=%s origin=%s stored_digest=%s",
                raw_result.outcome,
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


def _load_configured_path(
    path: Path, working_directory: str | None
) -> list[PreparedFlowDefinition]:
    if path.is_dir():
        return _load_flow_definition_directory(path, working_directory)
    if path.is_file():
        try:
            return [_load_flow_definition_file(path, working_directory)]
        except _InvalidFlowDefinition as failure:
            raise ValueError(str(failure)) from failure
    raise ValueError(f"Flow definition path {str(path)!r} does not exist")


def _load_flow_definition_directory(
    directory: Path, working_directory: str | None
) -> list[PreparedFlowDefinition]:
    candidates = sorted(
        (
            candidate
            for candidate in directory.rglob("*")
            if candidate.suffix.lower() == ".json" and candidate.is_file()
        ),
        key=str,
    )
    loaded: list[PreparedFlowDefinition] = []
    for candidate in candidates:
        try:
            loaded.append(_load_flow_definition_file(candidate, working_directory))
        except _InvalidFlowDefinition as failure:
            _LOGGER.info("skipping a file that is not a Flow definition: %s", failure)
    return loaded


def _load_flow_definition_file(
    file_path: Path, working_directory: str | None
) -> PreparedFlowDefinition:
    origin = f"file {str(file_path)!r}"
    try:
        content = file_path.read_bytes()
        modified_at_milliseconds = file_path.stat().st_mtime_ns // 1_000_000
    except OSError as failure:
        raise ValueError(
            f"cannot read Flow definition {origin}: {failure}"
        ) from failure
    flow_type, document_generated_at = parse_flow_definition_content(content, origin)
    generated_at = document_generated_at or (
        datetime.fromtimestamp(0, timezone.utc)
        + timedelta(milliseconds=modified_at_milliseconds)
    )
    return PreparedFlowDefinition(
        flow_type=flow_type,
        content=rewrite_source_path(content, working_directory),
        generated_at=generated_at,
        origin=origin,
    )


def _prepare_document(
    document: FlowDefinitionDocument, working_directory: str | None
) -> PreparedFlowDefinition:
    origin = f"in-memory document for Flow type {document.flow_type!r}"
    content = (
        document.content.encode("utf-8")
        if isinstance(document.content, str)
        else bytes(document.content)
    )
    try:
        # The content's generatedAt must be valid even though the caller's time is used.
        flow_name, _ = parse_flow_definition_content(content, origin)
    except _InvalidFlowDefinition as failure:
        raise ValueError(str(failure)) from failure
    if flow_name != document.flow_type:
        raise ValueError(
            f"Flow definition {origin} has flow.name {flow_name!r}; it must equal the Flow type"
        )
    return PreparedFlowDefinition(
        flow_type=document.flow_type,
        content=rewrite_source_path(content, working_directory),
        generated_at=truncate_to_milliseconds(document.generated_at),
        origin=origin,
    )


def parse_flow_definition_content(
    content: bytes, origin: str
) -> tuple[str, datetime | None]:
    """Validate a Flow Definition Graph 2.x document.

    Returns:
        The Flow type from ``flow.name`` and the ``generatedAt`` time truncated
        to milliseconds, or None when the document has no generatedAt.

    Raises:
        _InvalidFlowDefinition: The content is not strict UTF-8 JSON, not an
            object, has no 2.x schemaVersion, or has no non-empty flow.name.
        ValueError: The document is an FDG 2.x document with an invalid
            generatedAt.
    """
    document = _parse_flow_definition(content, origin)
    flow_type = _flow_name(document, origin)
    if "generatedAt" not in document:
        return flow_type, None
    return flow_type, _parse_generated_at(document["generatedAt"], origin)


def _parse_flow_definition(content: bytes, origin: str) -> dict[str, Any]:
    try:
        document = json.loads(
            content.decode("utf-8"), parse_constant=_reject_json_constant
        )
    except (UnicodeDecodeError, json.JSONDecodeError) as failure:
        raise _InvalidFlowDefinition(
            f"Flow definition {origin} is not UTF-8 JSON: {failure}"
        ) from failure
    if not isinstance(document, dict):
        raise _InvalidFlowDefinition(f"Flow definition {origin} is not a JSON object")
    schema_version = document.get("schemaVersion")
    if not isinstance(schema_version, str) or not schema_version.startswith(
        _SCHEMA_VERSION_PREFIX
    ):
        raise _InvalidFlowDefinition(
            f"Flow definition {origin} has schemaVersion {schema_version!r}; it must "
            'start with "2."'
        )
    return document


def _reject_json_constant(constant: str) -> Any:
    raise json.JSONDecodeError(f"{constant} is not JSON", constant, 0)


def _flow_name(document: dict[str, Any], origin: str) -> str:
    flow = document.get("flow")
    name = flow.get("name") if isinstance(flow, dict) else None
    if not isinstance(name, str) or not name:
        raise _InvalidFlowDefinition(
            f"Flow definition {origin} has no flow.name string"
        )
    return name


def _parse_generated_at(value: object, origin: str) -> datetime:
    if not isinstance(value, str):
        raise ValueError(
            f"Flow definition {origin} has a generatedAt that is not a string"
        )
    try:
        return parse_rfc3339_milliseconds(value)
    except ValueError as failure:
        raise ValueError(
            f"Flow definition {origin} has generatedAt {value!r}, which is not RFC 3339"
        ) from failure


def rewrite_source_path(content: bytes, working_directory: str | None) -> bytes:
    """Rewrite an absolute top-level ``source.path`` in validated FDG content.

    Returns:
        ``content`` itself unless the top-level object has exactly one
        ``source`` key whose object value has exactly one string ``path`` key
        that is absolute; then the content with only that string replaced.
    """
    text = content.decode("utf-8")
    source_members = [
        (value_start, value_end)
        for key, value_start, value_end in _object_members(
            text, _skip_whitespace(text, 0)
        )
        if key == "source"
    ]
    if len(source_members) != 1 or text[source_members[0][0]] != "{":
        return content
    path_members = [
        (value_start, value_end)
        for key, value_start, value_end in _object_members(text, source_members[0][0])
        if key == "path"
    ]
    if len(path_members) != 1 or text[path_members[0][0]] != '"':
        return content
    value_start, value_end = path_members[0]
    original_path, _ = _JSON_DECODER.raw_decode(text, value_start)
    shared_path = shareable_source_path(original_path, working_directory)
    if shared_path is None:
        return content
    rewritten = text[:value_start] + encode_json_string(shared_path) + text[value_end:]
    return rewritten.encode("utf-8")


def _working_directory() -> str | None:
    try:
        return os.getcwd()
    except OSError:
        return None


def _is_absolute_source_path(path: str) -> bool:
    return (
        path.startswith("/")
        or path.startswith("\\\\")
        or _ABSOLUTE_DRIVE_PATTERN.match(path) is not None
    )


def _lexical_path_segments(path: str) -> list[str]:
    """Split a path on both separators and resolve "." and ".." lexically.

    Returns:
        The root ("/", "//" for UNC, an uppercase drive such as "C:", or "")
        followed by the normalized segments, never popping above the root.
    """
    if _DRIVE_PATTERN.match(path) is not None:
        root, remainder = path[:2].upper(), path[2:]
    elif path.startswith("\\\\"):
        root, remainder = "//", path[2:]
    elif path.startswith("/"):
        root, remainder = "/", path
    else:
        root, remainder = "", path
    segments: list[str] = []
    for segment in _PATH_SEPARATOR_PATTERN.split(remainder):
        if segment in ("", "."):
            continue
        if segment == "..":
            if segments:
                segments.pop()
            continue
        segments.append(segment)
    return [root, *segments]


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
