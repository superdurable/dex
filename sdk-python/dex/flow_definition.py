# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from enum import Enum


@dataclass(frozen=True)
class FlowDefinitionDocument:
    """One in-memory Flow Definition Graph (FDG 2.0) JSON document to upload.

    Use it when the application builds the graph in memory instead of reading a
    file that ``dexcli visualize`` wrote. Pass documents to
    ``WorkerOptions(flow_definitions=...)`` so the Worker uploads them when it
    starts, or to ``Client.upload_flow_definitions`` from a deploy script.

    The Server keeps one document for each Flow type. It replaces a stored
    document only when the uploaded one has a different digest and a later
    ``generated_at``, so give a document the same ``generated_at`` every time its
    content is the same, such as the release time of the code that renders it.
    A changing ``generated_at`` with changing content lets the newest upload win.

    The SDK checks that ``content`` is a JSON object whose ``schemaVersion``
    starts with ``"2."`` and whose ``flow.name`` equals ``flow_type``. The Server
    also enforces its size and depth limits. Before uploading, the SDK rewrites
    an absolute top-level ``source.path`` inside the document to a path relative
    to the current working directory, or to the file name when the file is
    outside it or the directory is unknown, so local paths are not shared. The
    comparison is lexical: "." and ".." are resolved and symbolic links are not.
    It leaves the other bytes unchanged.

    .. versionadded:: 1.45.0

    Attributes:
        flow_type: The registered Flow type that the document describes. It must
            not be empty and must equal ``flow.name`` in ``content``.
        content: The FDG JSON document as UTF-8 bytes or as a string, which the
            SDK encodes as UTF-8.
        generated_at: When the document was generated. A naive datetime is
            treated as UTC. The SDK truncates it to whole milliseconds, as every
            Dex SDK does, so the same document gets the same time everywhere. The Server rejects a time more than 24 hours after
            its own clock and keeps microsecond precision.

    Examples:
        >>> from datetime import datetime, timezone
        >>> document = FlowDefinitionDocument(
        ...     flow_type="OrderFlow",
        ...     content=render_order_flow_graph(),
        ...     generated_at=datetime(2026, 11, 1, tzinfo=timezone.utc),
        ... )
        >>> worker = Worker(
        ...     registry, cache, WorkerOptions(flow_definitions=(document,))
        ... )
    """

    flow_type: str
    content: bytes | str
    generated_at: datetime

    def __post_init__(self) -> None:
        if not isinstance(self.flow_type, str) or not self.flow_type:
            raise ValueError("FlowDefinitionDocument.flow_type must not be empty")
        if not isinstance(self.content, (bytes, str)):
            raise TypeError("FlowDefinitionDocument.content must be bytes or str")
        if not isinstance(self.generated_at, datetime):
            raise TypeError("FlowDefinitionDocument.generated_at must be a datetime")


class FlowDefinitionUploadOutcome(Enum):
    """What the Server did with one uploaded Flow definition.

    .. versionadded:: 1.45.0

    Attributes:
        CREATED: No document was stored for the Flow type; the uploaded one is
            now stored.
        REPLACED: The uploaded document replaced a different, older one.
        UNCHANGED: The stored document has the same digest; nothing was written.
        KEPT_EXISTING: The stored document differs and its generated_at is the
            same or later, so it stays stored. Upload with ``force=True`` to
            replace it anyway.
        UNSPECIFIED: The Server returned no outcome or one this SDK version does
            not know, for example from a newer Server. Check ``stored_digest``
            and ``stored_generated_at`` for the stored document.

    Examples:
        >>> results = client.upload_flow_definitions([document])
        >>> if results[0].outcome is FlowDefinitionUploadOutcome.KEPT_EXISTING:
        ...     print("a newer definition is already stored")
    """

    CREATED = "created"
    REPLACED = "replaced"
    UNCHANGED = "unchanged"
    KEPT_EXISTING = "kept_existing"
    UNSPECIFIED = "unspecified"


@dataclass(frozen=True)
class FlowDefinitionUploadResult:
    """The outcome of uploading one Flow definition and the document stored afterwards.

    ``Client.upload_flow_definitions`` returns one result for each uploaded
    document, in request order.

    .. versionadded:: 1.45.0

    Attributes:
        flow_type: The Flow type of the uploaded document.
        outcome: What the Server did with the uploaded document.
        stored_digest: Lowercase hexadecimal SHA-256 digest of the document
            stored after the call. It differs from the uploaded document's digest
            only for ``KEPT_EXISTING``.
        stored_generated_at: ``generated_at`` of the document stored after the
            call, as an aware UTC datetime at microsecond precision.

    Examples:
        >>> for result in client.upload_flow_definitions(documents):
        ...     print(result.flow_type, result.outcome.value, result.stored_digest)
    """

    flow_type: str
    outcome: FlowDefinitionUploadOutcome
    stored_digest: str
    stored_generated_at: datetime
