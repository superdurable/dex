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
variables, the shared credentials file, or an instance or task role. Never
put credentials in session settings or Flow input.

Example:
    >>> store = S3MediaStore.from_store_uri("s3://agent-media/sessions")
    >>> reference = await store.put_image(Path("chart.png").read_bytes())
"""

from __future__ import annotations

import asyncio
import threading
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
    require_store_image_reference,
    verify_image_bytes,
)
from dex_agents.values import AgentImageReference

__all__ = ["S3MediaStore"]

_NOT_FOUND_ERROR_CODES = frozenset({"404", "NoSuchKey", "NotFound"})


class _S3Client(Protocol):
    def head_object(self, *, Bucket: str, Key: str) -> Mapping[str, Any]: ...

    def put_object(
        self, *, Bucket: str, Key: str, Body: bytes, ContentType: str
    ) -> Mapping[str, Any]: ...

    def get_object(self, *, Bucket: str, Key: str) -> Mapping[str, Any]: ...


class S3MediaStore:
    """Keep image bytes in an S3 bucket, one object per distinct image.

    The object key is ``<prefix>/sha256/<sha256>``, and a reference's ``uri`` is
    ``s3://<bucket>/<prefix>/sha256/<sha256>``. ``put_image`` checks whether the
    object exists and uploads it only when it does not, with the image's
    ``Content-Type``, so a retried upload writes nothing new. ``get_image``
    reads only objects under this store's bucket and prefix and checks the
    bytes against the reference.

    Session deletion does not delete objects. Expire old objects with a bucket
    lifecycle rule on the prefix.

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
    ) -> None:
        """Create a store; it makes no request until the first upload or read.

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

        Raises:
            ValueError: If ``bucket`` is empty or contains ``/``.
        """
        if not bucket or "/" in bucket:
            raise ValueError("S3MediaStore bucket must be non-empty and contain no /")
        self._bucket = bucket
        self._prefix = prefix.strip("/")
        self._store_uri = f"s3://{bucket}/{self._prefix}" if self._prefix else f"s3://{bucket}"
        self._endpoint_url = endpoint_url
        self._region_name = region_name
        self._config = Config(s3={"addressing_style": "path"}) if force_path_style else None
        self._client_lock = threading.Lock()
        self._client: _S3Client | None = None

    @classmethod
    def from_store_uri(
        cls,
        store_uri: str,
        *,
        endpoint_url: str | None = None,
        region_name: str | None = None,
        force_path_style: bool = False,
    ) -> S3MediaStore:
        """Create a store from ``s3://<bucket>`` or ``s3://<bucket>/<prefix>``.

        Args:
            store_uri: The bucket and optional prefix as an S3 URI.
            endpoint_url: See ``__init__``.
            region_name: See ``__init__``.
            force_path_style: See ``__init__``.

        Returns:
            The store.

        Raises:
            ValueError: If ``store_uri`` does not start with ``s3://`` or names
                no bucket.
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
            ImageValidationError: If the bytes are empty, too large, or of
                another type. Nothing is uploaded.
            botocore.exceptions.ClientError: If S3 refuses the request, such as
                for missing credentials or permissions. Retrying is safe.
        """
        reference = build_image_reference(data, self._store_uri)
        key = self.object_key(reference.sha256)
        await asyncio.to_thread(self._put_object_if_absent, key, bytes(data), reference.mime_type)
        return reference

    async def get_image(self, reference: AgentImageReference) -> bytes:
        """Return the bytes of one image in this store.

        Args:
            reference: A reference under this store's bucket and prefix.

        Returns:
            The exact bytes, checked against ``sha256`` and ``size_bytes``.

        Raises:
            ImageValidationError: If the reference's fields are invalid.
            MediaStoreError: If the reference is outside this store's bucket
                and prefix, the object is missing, or its bytes do not match.
            botocore.exceptions.ClientError: If S3 refuses the request for
                another reason.
        """
        require_store_image_reference(reference, self._store_uri)
        data = await asyncio.to_thread(
            self._get_object, self.object_key(reference.sha256), reference.uri
        )
        return verify_image_bytes(reference, data)

    def _s3_client(self) -> _S3Client:
        # Creating a client resolves credentials, which can read files, so it runs on first use.
        with self._client_lock:
            if self._client is None:
                self._client = cast(
                    _S3Client,
                    boto3.session.Session().client(
                        "s3",
                        endpoint_url=self._endpoint_url,
                        region_name=self._region_name,
                        config=self._config,
                    ),
                )
            return self._client

    def _put_object_if_absent(self, key: str, data: bytes, mime_type: str) -> None:
        client = self._s3_client()
        try:
            client.head_object(Bucket=self._bucket, Key=key)
            return
        except ClientError as error:
            if not _is_not_found(error):
                raise
        client.put_object(Bucket=self._bucket, Key=key, Body=data, ContentType=mime_type)

    def _get_object(self, key: str, uri: str) -> bytes:
        try:
            response = self._s3_client().get_object(Bucket=self._bucket, Key=key)
        except ClientError as error:
            if _is_not_found(error):
                raise MediaStoreError(f"image {uri} is not in the media store") from error
            raise
        body = response["Body"]
        try:
            return cast(bytes, body.read())
        finally:
            body.close()


def _is_not_found(error: Any) -> bool:
    return str(error.response.get("Error", {}).get("Code", "")) in _NOT_FOUND_ERROR_CODES
