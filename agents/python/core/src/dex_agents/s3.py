# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""``S3MediaStore``: image bytes of agent sessions in an S3-compatible bucket.

Install the extra with ``pip install "dex-agents[s3]"``. The store uses
boto3, which reads credentials from the standard AWS chain: environment
variables, the shared credentials file, or an instance or task role. Pass
``access_key_id`` and ``secret_access_key`` only for a bucket whose keys are
not secret, such as a local development bucket. Never put credentials in
session settings or Flow input.

Example:
    >>> store = S3MediaStore.from_store_uri("s3://agent-media/sessions")
    >>> reference = await store.put_image(Path("chart.png").read_bytes())
"""

from __future__ import annotations

import asyncio
import threading
import time
from collections.abc import Mapping
from typing import Any, Protocol, cast

try:
    import boto3  # type: ignore[import-untyped]
    from botocore.config import Config  # type: ignore[import-untyped]
    from botocore.exceptions import ClientError  # type: ignore[import-untyped]
except ImportError as error:  # pragma: no cover - exercised by the built-package smoke test
    raise ImportError(
        "dex_agents.s3 needs boto3; install it with: pip install 'dex-agents[s3]'"
    ) from error

from dex_agents.media import (
    MediaStoreError,
    build_image_reference,
    require_short_image_uris,
    require_store_image_reference,
    verify_image_bytes,
)
from dex_agents.values import AgentImageReference

__all__ = ["S3MediaStore"]

_NOT_FOUND_ERROR_CODES = frozenset({"404", "NoSuchKey", "NotFound"})
_ALREADY_EXISTS_ERROR_CODES = frozenset({"412", "PreconditionFailed"})
# A concurrent write or delete of the key; S3 asks the caller to retry the upload.
_CONDITIONAL_WRITE_CONFLICT_ERROR_CODES = frozenset({"409", "ConditionalRequestConflict"})
# Seconds before each retry of a conditional PUT that S3 answered with 409.
_CONDITIONAL_WRITE_CONFLICT_RETRY_DELAYS = (0.1, 0.4)
_CONDITIONAL_WRITE_UNSUPPORTED_ERROR_CODES = frozenset({"501", "NotImplemented"})


class _S3Client(Protocol):
    def head_object(self, *, Bucket: str, Key: str) -> Mapping[str, Any]: ...

    def put_object(self, **parameters: Any) -> Mapping[str, Any]: ...

    def get_object(self, *, Bucket: str, Key: str) -> Mapping[str, Any]: ...


class S3MediaStore:
    """Keep image bytes in an S3 bucket, one object per distinct image.

    The object key is ``<prefix>/sha256/<sha256>``, and a reference's ``uri`` is
    ``s3://<bucket>/<prefix>/sha256/<sha256>``. ``put_image`` uploads with
    ``If-None-Match: *`` and the image's ``Content-Type``, and treats "already
    exists" (412) as success, so concurrent and retried uploads of the same
    bytes write one object. A 409 conflict, from a concurrent write or delete of
    the key, retries the upload twice with a short backoff; if every attempt
    conflicts, a ``HEAD`` must find the object with the image's size, or
    ``put_image`` raises ``MediaStoreError``. A service that refuses conditional writes gets a
    ``HEAD`` first and a plain ``PUT`` when the object is missing; then two
    concurrent uploads may both write, so deduplication holds for the key and
    its final content, not for every write. ``get_image`` reads only objects
    under this store's bucket and prefix, refuses an object whose
    ``Content-Length`` differs from the reference, reads at most one byte more
    than ``size_bytes``, and checks the bytes with ``verify_image_bytes``.

    Session deletion does not delete objects. A runner reads a session's images
    again whenever a model call replays them, so keep every object at least as
    long as any session that references it can still run a turn or replay its
    history. A lifecycle rule that expires objects sooner makes those turns
    fail with ``MediaStoreError``.

    Example:
        >>> store = S3MediaStore("agent-media", "sessions")
        >>> reference = await store.put_image(screenshot_png)
        >>> reference.uri
        's3://agent-media/sessions/sha256/9f2c...'

    .. versionadded:: 0.5.0
    """

    def __init__(
        self,
        bucket: str,
        prefix: str = "",
        *,
        endpoint_url: str | None = None,
        region_name: str | None = None,
        force_path_style: bool = False,
        access_key_id: str | None = None,
        secret_access_key: str | None = None,
        session_token: str | None = None,
    ) -> None:
        """Create a store; it makes no request until the first upload or read.

        Example:
            >>> # A local development bucket with fixed loopback credentials.
            >>> S3MediaStore("agent-media", endpoint_url="http://127.0.0.1:9000",
            ...              region_name="us-east-1", force_path_style=True,
            ...              access_key_id="s3local", secret_access_key="s3local")

        Args:
            bucket: The bucket name. It must be non-empty and contain no ``/``.
            prefix: Key prefix of every object, such as ``"agents/media"``.
                Leading and trailing ``/`` characters are dropped. Empty, the
                default, keeps objects at ``sha256/<sha256>``.
            endpoint_url: Endpoint of an S3-compatible service, such as
                ``"http://127.0.0.1:9000"``, or ``None`` for AWS S3.
            region_name: AWS region, or ``None`` for the standard AWS
                configuration, such as ``AWS_REGION``.
            force_path_style: Whether to address the bucket in the URL path
                instead of the host name. Many S3-compatible services need it.
            access_key_id: Access key ID to sign requests with, or ``None``,
                the default, for the standard AWS credential chain. Set it
                together with ``secret_access_key``.
            secret_access_key: Secret access key for ``access_key_id``. The
                store never logs it or shows it in its ``repr``.
            session_token: Session token of temporary credentials, or ``None``.
                It needs ``access_key_id``.

        Raises:
            ValueError: If ``bucket`` is empty or contains ``/``, if the bucket
                and prefix make image URIs longer than
                ``MAX_IMAGE_URI_LENGTH``, or if only one of ``access_key_id``
                and ``secret_access_key`` is set, or ``session_token`` without
                them.
        """
        if not bucket or "/" in bucket:
            raise ValueError("S3MediaStore bucket must be non-empty and contain no /")
        if (access_key_id is None) != (secret_access_key is None) or (
            session_token is not None and access_key_id is None
        ):
            raise ValueError(
                "S3MediaStore takes access_key_id and secret_access_key together, "
                "and session_token only with them"
            )
        self._bucket = bucket
        self._prefix = prefix.strip("/")
        self._store_uri = f"s3://{bucket}/{self._prefix}" if self._prefix else f"s3://{bucket}"
        require_short_image_uris(self._store_uri)
        self._client_parameters: dict[str, Any] = {
            "endpoint_url": endpoint_url,
            "region_name": region_name,
            "config": Config(s3={"addressing_style": "path"}) if force_path_style else None,
            "aws_access_key_id": access_key_id,
            "aws_secret_access_key": secret_access_key,
            "aws_session_token": session_token,
        }
        self._client_lock = threading.Lock()
        self._client: _S3Client | None = None
        self._supports_conditional_writes = True

    def __repr__(self) -> str:
        return f"S3MediaStore({self._store_uri!r})"

    @classmethod
    def from_store_uri(
        cls,
        store_uri: str,
        *,
        endpoint_url: str | None = None,
        region_name: str | None = None,
        force_path_style: bool = False,
        access_key_id: str | None = None,
        secret_access_key: str | None = None,
        session_token: str | None = None,
    ) -> S3MediaStore:
        """Create a store from ``s3://<bucket>`` or ``s3://<bucket>/<prefix>``.

        Args:
            store_uri: The bucket and optional prefix as an S3 URI.
            endpoint_url: See ``__init__``.
            region_name: See ``__init__``.
            force_path_style: See ``__init__``.
            access_key_id: See ``__init__``.
            secret_access_key: See ``__init__``.
            session_token: See ``__init__``.

        Returns:
            The store.

        Raises:
            ValueError: If ``store_uri`` does not start with ``s3://`` or names
                no bucket, or for the reasons ``__init__`` gives.
        """
        if not store_uri.startswith("s3://"):
            raise ValueError(f"S3 media store URI must start with s3://, got {store_uri!r}")
        bucket, _, prefix = store_uri.removeprefix("s3://").partition("/")
        return cls(
            bucket,
            prefix,
            endpoint_url=endpoint_url,
            region_name=region_name,
            force_path_style=force_path_style,
            access_key_id=access_key_id,
            secret_access_key=secret_access_key,
            session_token=session_token,
        )

    @property
    def store_uri(self) -> str:
        """Return the location that every reference of this store starts with.

        Returns:
            ``"s3://<bucket>/<prefix>"``, or ``"s3://<bucket>"`` without a prefix.
        """
        return self._store_uri

    def object_key(self, sha256: str) -> str:
        """Return the object key of the image with this SHA-256.

        Args:
            sha256: SHA-256 of the bytes as 64 lowercase hex digits.

        Returns:
            ``"<prefix>/sha256/<sha256>"``, or ``"sha256/<sha256>"`` without a prefix.
        """
        return f"{self._prefix}/sha256/{sha256}" if self._prefix else f"sha256/{sha256}"

    async def put_image(self, data: bytes) -> AgentImageReference:
        """Store one image unless the bucket already has it, and return its reference.

        Args:
            data: PNG, JPEG, GIF, or WebP bytes, at most ``MAX_IMAGE_BYTES``.

        Returns:
            The reference.

        Raises:
            ImageValidationError: If the bytes are not a valid image for
                ``build_image_reference``. Nothing is uploaded.
            MediaStoreError: If every conditional upload conflicted and the
                object is still missing, or has another size. Retrying is safe.
            botocore.exceptions.ClientError: If S3 refuses the request, such as
                for missing credentials or permissions. Retrying is safe.
        """
        reference = build_image_reference(data, self._store_uri)
        await asyncio.to_thread(self._put_object_if_absent, reference, bytes(data))
        return reference

    async def get_image(self, reference: AgentImageReference) -> bytes:
        """Return the bytes of one image in this store.

        Args:
            reference: A reference under this store's bucket and prefix.

        Returns:
            The exact bytes, checked with ``verify_image_bytes``.

        Raises:
            ImageValidationError: If the reference's fields are invalid.
            MediaStoreError: If the reference is outside this store's bucket
                and prefix, the object is missing, or its bytes do not match.
            botocore.exceptions.ClientError: If S3 refuses the request for
                another reason.
        """
        require_store_image_reference(reference, self._store_uri)
        data = await asyncio.to_thread(self._get_object, reference)
        return verify_image_bytes(reference, data)

    def _s3_client(self) -> _S3Client:
        # Creating a client resolves credentials, which can read files, so it runs on first use.
        with self._client_lock:
            if self._client is None:
                self._client = cast(
                    _S3Client, boto3.session.Session().client("s3", **self._client_parameters)
                )
            return self._client

    def _put_object_if_absent(self, reference: AgentImageReference, data: bytes) -> None:
        client = self._s3_client()
        key = self.object_key(reference.sha256)
        parameters: dict[str, Any] = {
            "Bucket": self._bucket,
            "Key": key,
            "Body": data,
            "ContentType": reference.mime_type,
        }
        if self._supports_conditional_writes:
            for retry_delay in (*_CONDITIONAL_WRITE_CONFLICT_RETRY_DELAYS, None):
                try:
                    client.put_object(**parameters, IfNoneMatch="*")
                    return
                except ClientError as error:
                    if _has_error_code(error, _ALREADY_EXISTS_ERROR_CODES):
                        return
                    if _has_error_code(error, _CONDITIONAL_WRITE_UNSUPPORTED_ERROR_CODES):
                        self._supports_conditional_writes = False
                        break
                    if not _has_error_code(error, _CONDITIONAL_WRITE_CONFLICT_ERROR_CODES):
                        raise
                if retry_delay is None:
                    # Every attempt conflicted: return only a reference whose object exists.
                    if self._stored_size(key) != reference.size_bytes:
                        raise MediaStoreError(
                            f"image {reference.uri} was not stored: every upload conflicted "
                            "with another write"
                        )
                    return
                time.sleep(retry_delay)
        if self._stored_size(key) is None:
            client.put_object(**parameters)

    def _stored_size(self, key: str) -> int | None:
        """Return the size of the object at ``key``, or ``None`` when there is none."""
        try:
            response = self._s3_client().head_object(Bucket=self._bucket, Key=key)
        except ClientError as error:
            if _has_error_code(error, _NOT_FOUND_ERROR_CODES):
                return None
            raise
        return int(response.get("ContentLength", -1))

    def _get_object(self, reference: AgentImageReference) -> bytes:
        try:
            response = self._s3_client().get_object(
                Bucket=self._bucket, Key=self.object_key(reference.sha256)
            )
        except ClientError as error:
            if _has_error_code(error, _NOT_FOUND_ERROR_CODES):
                raise MediaStoreError(f"image {reference.uri} is not in the media store") from error
            raise
        body = response["Body"]
        try:
            content_length = response.get("ContentLength")
            if content_length is not None and content_length != reference.size_bytes:
                raise MediaStoreError(f"image {reference.uri} does not match its sha256 and size")
            # One byte more than expected tells an oversized body apart without reading all of it.
            data = cast(bytes, body.read(reference.size_bytes + 1))
        finally:
            body.close()
        if len(data) > reference.size_bytes:
            raise MediaStoreError(f"image {reference.uri} does not match its sha256 and size")
        return data


def _has_error_code(error: Any, codes: frozenset[str]) -> bool:
    response = error.response
    code = str(response.get("Error", {}).get("Code", ""))
    status = str(response.get("ResponseMetadata", {}).get("HTTPStatusCode", ""))
    return code in codes or status in codes
