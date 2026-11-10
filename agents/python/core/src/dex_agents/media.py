# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Images in agent sessions: references in session state, bytes in a media store.

A session stores only ``AgentImageReference`` values. The bytes live in a
``MediaStore``, keyed by their SHA-256, so an upload is idempotent and a
reference always reads back the bytes it was made from. ``S3MediaStore`` in
``dex_agents.s3`` keeps them in an S3-compatible bucket, and
``InMemoryMediaStore`` keeps them in the process for tests.

Example:
    >>> store = InMemoryMediaStore()
    >>> reference = await store.put_image(Path("chart.png").read_bytes())
    >>> await sessions.send_user_message("session-42", "What does this chart show?",
    ...                                  images=[reference])
"""

from __future__ import annotations

import asyncio
import hashlib
import re
from collections.abc import Iterable, Mapping, Sequence
from typing import Protocol

from dex_agents.runner import AgentTurnContext
from dex_agents.values import AgentImageReference

#: Largest image a media store accepts, in bytes. Its base64 text is 5,000,000
#: bytes, which fits the per-image limits of Amazon Bedrock (5 MB of base64)
#: and the Claude API (10 MB of base64).
#:
#: .. versionadded:: 0.5.0
MAX_IMAGE_BYTES = 3_750_000

#: Most images one user message or one tool result may carry.
#:
#: .. versionadded:: 0.5.0
MAX_IMAGES_PER_MESSAGE = 20

#: Image types a media store accepts, detected from the bytes.
#:
#: .. versionadded:: 0.5.0
SUPPORTED_IMAGE_MIME_TYPES = ("image/png", "image/jpeg", "image/gif", "image/webp")

#: Longest ``AgentImageReference.uri`` in bytes as UTF-8, the same unit in
#: every core; S3 object keys are at most 1024 bytes.
#:
#: .. versionadded:: 0.5.0
MAX_IMAGE_URI_LENGTH = 1024

_SHA256_PATTERN = re.compile(r"[0-9a-f]{64}")
_IMAGE_URI_PATTERN = re.compile(r"[a-z][a-z0-9+.\-]*://\S+/sha256/[0-9a-f]{64}")
_PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"
_PNG_HEADER_CHUNK = b"\x00\x00\x00\x0dIHDR"
_WEBP_CHUNK_TYPES = (b"VP8 ", b"VP8L", b"VP8X")
_FORMAT_NAMES = {"image/png": "PNG", "image/jpeg": "JPEG", "image/gif": "GIF", "image/webp": "WebP"}


class ImageValidationError(ValueError):
    """Report image bytes or an image reference that Dex agents refuse.

    ``put_image`` raises it for bytes that are empty, larger than
    ``MAX_IMAGE_BYTES``, or not PNG, JPEG, GIF, or WebP.
    ``validate_image_reference`` and ``validate_image_references`` raise it for
    a reference whose fields are invalid. The message says what is wrong.

    .. versionadded:: 0.5.0
    """


class MediaStoreError(Exception):
    """Report that a media store cannot return the bytes of a valid reference.

    ``get_image`` raises it when the reference belongs to another store, when
    the store has no object for it, or when the stored bytes do not match the
    reference's SHA-256, size, and type.

    .. versionadded:: 0.5.0
    """


class MediaStore(Protocol):
    """Keep image bytes outside session state, addressed by their content.

    Implementations key every object by the SHA-256 of its bytes, under a
    location of their own, so ``put_image`` is idempotent: the same bytes give
    the same reference and one object, however often a retried tool Step or
    client uploads them. Use ``S3MediaStore`` for an S3-compatible bucket or
    ``InMemoryMediaStore`` in tests. Upload with ``put_image`` before a reference
    enters a message or a tool result, and read with ``get_image`` or
    ``load_images`` when building model input.

    Example:
        >>> reference = await store.put_image(screenshot_png)
        >>> return ToolExecutionResult(output="Took a screenshot.", images=[reference])

    .. versionadded:: 0.5.0
    """

    async def put_image(self, data: bytes) -> AgentImageReference:
        """Store one image and return its reference.

        Args:
            data: PNG, JPEG, GIF, or WebP bytes, at most ``MAX_IMAGE_BYTES``.

        Returns:
            The reference, whose ``uri`` ends in ``/sha256/<sha256>``.

        Raises:
            ImageValidationError: If the bytes are empty, too large, or of
                another type. Nothing is stored.
            Exception: The store's own error when the upload fails; uploading
                the same bytes again is safe.
        """
        ...

    async def get_image(self, reference: AgentImageReference) -> bytes:
        """Return the bytes of one image this store holds.

        Args:
            reference: A reference that this store's ``put_image`` returned.

        Returns:
            The exact bytes, checked with ``verify_image_bytes`` against
            ``sha256``, ``size_bytes``, and ``mime_type``.

        Raises:
            ImageValidationError: If the reference's fields are invalid.
            MediaStoreError: If the reference belongs to another store, the
                object is missing, or its bytes do not match the reference.
            Exception: The store's own error when the read fails.
        """
        ...


class InMemoryMediaStore:
    """Keep images in this process, for tests and single-process tools.

    References use the ``memory://<name>`` location. Two stores with the same
    name accept each other's references, but each holds only what was put into
    it, so a Worker in another process cannot read them.

    Example:
        >>> store = InMemoryMediaStore()
        >>> reference = await store.put_image(png_bytes)
        >>> assert await store.get_image(reference) == png_bytes

    .. versionadded:: 0.5.0
    """

    def __init__(self, name: str = "default") -> None:
        """Create an empty store.

        Args:
            name: The location name in ``memory://<name>``. It must be
                non-empty and contain no ``/`` or whitespace.

        Raises:
            ValueError: If ``name`` is empty, contains ``/`` or whitespace, or
                makes image URIs longer than ``MAX_IMAGE_URI_LENGTH``.
        """
        if not name or "/" in name or any(character.isspace() for character in name):
            raise ValueError("InMemoryMediaStore name must be non-empty, without / or whitespace")
        self._store_uri = f"memory://{name}"
        require_short_image_uris(self._store_uri)
        self._images: dict[str, bytes] = {}

    @property
    def store_uri(self) -> str:
        """Return the location that every reference of this store starts with.

        Returns:
            ``"memory://<name>"``.
        """
        return self._store_uri

    @property
    def image_count(self) -> int:
        """Return how many distinct images the store holds.

        Returns:
            The number of objects; uploading the same bytes twice counts once.
        """
        return len(self._images)

    async def put_image(self, data: bytes) -> AgentImageReference:
        """Store one image and return its reference. See ``MediaStore.put_image``.

        Args:
            data: PNG, JPEG, GIF, or WebP bytes, at most ``MAX_IMAGE_BYTES``.

        Returns:
            The reference.

        Raises:
            ImageValidationError: If the bytes are empty, too large, or of
                another type.
        """
        reference = build_image_reference(data, self._store_uri)
        self._images.setdefault(reference.sha256, bytes(data))
        return reference

    async def get_image(self, reference: AgentImageReference) -> bytes:
        """Return the bytes of one image. See ``MediaStore.get_image``.

        Args:
            reference: A reference that this store's ``put_image`` returned.

        Returns:
            The exact bytes.

        Raises:
            ImageValidationError: If the reference's fields are invalid.
            MediaStoreError: If the reference belongs to another store, the
                store does not hold the image, or its bytes do not match.
        """
        require_store_image_reference(reference, self._store_uri)
        data = self._images.get(reference.sha256)
        if data is None:
            raise MediaStoreError(f"image {reference.uri} is not in the media store")
        return verify_image_bytes(reference, data)


def detect_image_mime_type(data: bytes) -> str:
    """Return the MIME type of image bytes, read from their structure.

    The signature picks the type, then a cheap structural check must hold; it
    does not decode the image. PNG needs its signature followed by the
    ``IHDR`` chunk. JPEG needs SOI, a marker right after it, and EOI as the
    last two bytes. GIF needs a ``GIF87a`` or ``GIF89a`` header and the
    logical screen descriptor. WebP needs a RIFF size equal to the byte count
    minus 8 and a first chunk of ``VP8 ``, ``VP8L``, or ``VP8X``.

    Args:
        data: Image bytes.

    Returns:
        ``"image/png"``, ``"image/jpeg"``, ``"image/gif"``, or ``"image/webp"``.

    Raises:
        ImageValidationError: If the bytes are empty, start with no supported
            signature, or fail the structural check of their type.

    .. versionadded:: 0.5.0
    """
    if not data:
        raise ImageValidationError("image is empty")
    mime_type = _signature_mime_type(data)
    if mime_type is None:
        raise ImageValidationError("image is not PNG, JPEG, GIF, or WebP")
    if not _is_well_formed(mime_type, data):
        raise ImageValidationError(f"image is not a well-formed {_FORMAT_NAMES[mime_type]}")
    return mime_type


def _signature_mime_type(data: bytes) -> str | None:
    if data.startswith(_PNG_SIGNATURE):
        return "image/png"
    if data.startswith(b"\xff\xd8\xff"):
        return "image/jpeg"
    if data.startswith((b"GIF87a", b"GIF89a")):
        return "image/gif"
    if data[:4] == b"RIFF" and data[8:12] == b"WEBP":
        return "image/webp"
    return None


def _is_well_formed(mime_type: str, data: bytes) -> bool:
    if mime_type == "image/png":
        return len(data) >= 33 and data[8:16] == _PNG_HEADER_CHUNK
    if mime_type == "image/jpeg":
        return len(data) >= 6 and 0xC0 <= data[3] <= 0xFE and data.endswith(b"\xff\xd9")
    if mime_type == "image/gif":
        return len(data) >= 13
    riff_size = int.from_bytes(data[4:8], "little")
    return len(data) >= 20 and riff_size + 8 == len(data) and data[12:16] in _WEBP_CHUNK_TYPES


def build_image_reference(data: bytes, store_uri: str) -> AgentImageReference:
    """Check image bytes and return the reference a store keeps them under.

    A ``MediaStore`` implementation calls it before it uploads: the reference
    names the object, ``image_uri(store_uri, sha256)``.

    Args:
        data: Image bytes.
        store_uri: The store's location, such as ``"s3://media/agents"``.

    Returns:
        The reference, with the detected MIME type, SHA-256, and size.

    Raises:
        ImageValidationError: If the bytes are empty, larger than
            ``MAX_IMAGE_BYTES``, or not a well-formed PNG, JPEG, GIF, or WebP,
            or if ``validate_image_reference`` refuses the reference, such as
            for a URI longer than ``MAX_IMAGE_URI_LENGTH``.

    .. versionadded:: 0.5.0
    """
    if len(data) > MAX_IMAGE_BYTES:
        raise ImageValidationError(
            f"image is {len(data)} bytes; the limit is {MAX_IMAGE_BYTES} bytes"
        )
    mime_type = detect_image_mime_type(data)
    sha256 = hashlib.sha256(data).hexdigest()
    reference = AgentImageReference(
        uri=image_uri(store_uri, sha256),
        mime_type=mime_type,
        sha256=sha256,
        size_bytes=len(data),
    )
    validate_image_reference(reference)
    return reference


def image_uri(store_uri: str, sha256: str) -> str:
    """Return the URI of the image with this SHA-256 in a store.

    Args:
        store_uri: The store's location; trailing ``/`` characters are dropped.
        sha256: SHA-256 of the bytes as 64 lowercase hex digits.

    Returns:
        ``"<store_uri>/sha256/<sha256>"``.

    .. versionadded:: 0.5.0
    """
    return f"{store_uri.rstrip('/')}/sha256/{sha256}"


def require_short_image_uris(store_uri: str) -> None:
    """Check that every image URI of a store fits ``MAX_IMAGE_URI_LENGTH``.

    A ``MediaStore`` implementation calls it in its constructor, so a long
    location fails at once instead of on the first upload.

    Args:
        store_uri: The store's location.

    Raises:
        ValueError: If ``image_uri(store_uri, sha256)`` is longer than
            ``MAX_IMAGE_URI_LENGTH`` bytes as UTF-8.

    .. versionadded:: 0.5.0
    """
    if _utf8_length(image_uri(store_uri, "0" * 64)) > MAX_IMAGE_URI_LENGTH:
        raise ValueError(
            f"media store location {store_uri} makes image URIs longer than "
            f"{MAX_IMAGE_URI_LENGTH} bytes as UTF-8"
        )


def validate_image_reference(reference: AgentImageReference) -> None:
    """Check the fields of one image reference.

    The checks run in order, and the first failure is raised: ``type`` is
    ``"image"``; ``mime_type`` is supported; ``sha256`` is 64 lowercase hex
    digits; ``size_bytes`` is from 1 to ``MAX_IMAGE_BYTES``; ``uri`` is at
    most ``MAX_IMAGE_URI_LENGTH`` bytes as UTF-8, looks like
    ``<scheme>://<location>/sha256/<sha256>``, and ends with the reference's
    own ``sha256``. It does not read the store.

    Args:
        reference: The reference, such as one decoded from a request.

    Raises:
        ImageValidationError: If a field is invalid.

    .. versionadded:: 0.5.0
    """
    if reference.type != "image":
        raise ImageValidationError('image reference type must be "image"')
    if reference.mime_type not in SUPPORTED_IMAGE_MIME_TYPES:
        raise ImageValidationError(
            "image reference mime_type must be one of " + ", ".join(SUPPORTED_IMAGE_MIME_TYPES)
        )
    if not isinstance(reference.sha256, str) or not _SHA256_PATTERN.fullmatch(reference.sha256):
        raise ImageValidationError("image reference sha256 must be 64 lowercase hex digits")
    size = reference.size_bytes
    if not isinstance(size, int) or isinstance(size, bool) or not 1 <= size <= MAX_IMAGE_BYTES:
        raise ImageValidationError(
            f"image reference size_bytes must be from 1 to {MAX_IMAGE_BYTES}"
        )
    uri = reference.uri
    if not isinstance(uri, str) or _utf8_length(uri) > MAX_IMAGE_URI_LENGTH:
        raise ImageValidationError(
            f"image reference uri must be at most {MAX_IMAGE_URI_LENGTH} bytes as UTF-8"
        )
    if not _IMAGE_URI_PATTERN.fullmatch(uri):
        raise ImageValidationError(
            "image reference uri must look like <scheme>://<location>/sha256/<sha256>"
        )
    if not uri.endswith("/" + reference.sha256):
        raise ImageValidationError("image reference uri must end with its own sha256")


def validate_image_references(references: Sequence[AgentImageReference]) -> None:
    """Check the images of one user message or one tool result.

    Args:
        references: The images, at most ``MAX_IMAGES_PER_MESSAGE``.

    Raises:
        ImageValidationError: If there are too many images, or with
            ``"images[<index>]: "`` before the reason when one is invalid.

    .. versionadded:: 0.5.0
    """
    if len(references) > MAX_IMAGES_PER_MESSAGE:
        raise ImageValidationError(
            f"a message may carry at most {MAX_IMAGES_PER_MESSAGE} images, got {len(references)}"
        )
    for index, reference in enumerate(references):
        try:
            validate_image_reference(reference)
        except ImageValidationError as error:
            raise ImageValidationError(f"images[{index}]: {error}") from None


def require_store_image_reference(reference: AgentImageReference, store_uri: str) -> None:
    """Check that a valid reference names an image of the store at ``store_uri``.

    A ``MediaStore`` implementation calls it first in ``get_image``, so a
    request cannot make the Worker read outside the store's location.

    Args:
        reference: The reference to read.
        store_uri: The store's location.

    Raises:
        ImageValidationError: If the reference's fields are invalid.
        MediaStoreError: If its ``uri`` is not ``image_uri(store_uri, sha256)``.

    .. versionadded:: 0.5.0
    """
    validate_image_reference(reference)
    if reference.uri != image_uri(store_uri, reference.sha256):
        raise MediaStoreError(f"image {reference.uri} is not in the media store {store_uri}")


def verify_image_bytes(reference: AgentImageReference, data: bytes) -> bytes:
    """Check bytes read from a store against their reference.

    The SHA-256 binds the bytes, and ``detect_image_mime_type`` must find the
    reference's ``mime_type`` in them, so a reference whose type was changed
    after upload fails here.

    Args:
        reference: The reference that was read.
        data: The bytes the store returned.

    Returns:
        ``data``, unchanged.

    Raises:
        MediaStoreError: If the size or the SHA-256 differs, or if the bytes
            are not a well-formed image of ``mime_type``.

    .. versionadded:: 0.5.0
    """
    if len(data) != reference.size_bytes or hashlib.sha256(data).hexdigest() != reference.sha256:
        raise MediaStoreError(f"image {reference.uri} does not match its sha256 and size")
    try:
        detected = detect_image_mime_type(data)
    except ImageValidationError:
        detected = None
    if detected != reference.mime_type:
        raise MediaStoreError(
            f"image {reference.uri} does not hold a well-formed {reference.mime_type}"
        )
    return data


async def load_images(
    store: MediaStore, references: Iterable[AgentImageReference]
) -> Mapping[str, bytes]:
    """Read the bytes of several images, each distinct image once.

    A runner calls it while building model input, with the references it
    chose from ``collect_turn_images`` or from its own replayed items. The
    reads run concurrently.

    Example:
        >>> images = await load_images(store, collect_turn_images(turn))
        >>> parts = [image_part(reference.mime_type, images[reference.sha256])
        ...          for reference in message.images]

    Args:
        store: The store that holds the images.
        references: The images to read, in any order, with repeats allowed.

    Returns:
        The bytes of each image, keyed by ``AgentImageReference.sha256``.

    Raises:
        ImageValidationError: If a reference's fields are invalid.
        MediaStoreError: If the store cannot return an image's exact bytes.
        Exception: The store's own error when a read fails.

    .. versionadded:: 0.5.0
    """
    distinct = {reference.sha256: reference for reference in references}
    contents = await asyncio.gather(
        *(store.get_image(reference) for reference in distinct.values())
    )
    return dict(zip(distinct, contents, strict=True))


def collect_turn_images(turn: AgentTurnContext) -> list[AgentImageReference]:
    """Return every image a model call of this turn may show, each once.

    It collects ``AgentMessage.images`` of ``turn.messages`` in sequence
    order, then ``AgentToolCall.images`` of the turn's records in deferral
    order, keeping the first reference to each SHA-256. Whether a model call
    shows earlier turns' images is the runner's choice; pass only the
    references it needs to ``load_images``.

    Args:
        turn: The context of one ``run_model_step`` call.

    Returns:
        The references, without repeats.

    .. versionadded:: 0.5.0
    """
    collected: dict[str, AgentImageReference] = {}
    for message in turn.messages:
        for reference in message.images:
            collected.setdefault(reference.sha256, reference)
    for record in sorted(turn.tool_calls.values(), key=lambda record: record.deferral_order):
        for reference in record.images:
            collected.setdefault(reference.sha256, reference)
    return list(collected.values())


def _utf8_length(text: str) -> int:
    # A lone surrogate counts 3 bytes, as Buffer.byteLength counts it in TypeScript.
    return len(text.encode("utf-8", "surrogatepass"))
