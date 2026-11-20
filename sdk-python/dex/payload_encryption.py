# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Encrypt Dex payloads on the client before the Server stores them.

Configure :class:`PayloadEncryptionOptions` on ``ClientOptions`` and
``WorkerOptions``. The SDK then encrypts every Value it sends and decrypts
every Value it receives. The Server stores only ciphertext and never holds
keys. The wire format is ``dex/1``, specified in ``protos/payload-encryption.md``
of the Dex repository.

.. versionadded:: 1.45.10
"""

from __future__ import annotations

import asyncio
import base64
import binascii
import json
import logging
import os
import stat
import zlib
from collections.abc import Sequence
from dataclasses import dataclass
from enum import Enum
from pathlib import Path
from typing import Any, Protocol

from dex.dexpb import dex_pb2 as pb
from dex.runtime_errors import ValueMappingError

_LOGGER = logging.getLogger(__name__)

#: The default largest serialized ``PayloadEnvelope`` the SDK reads or writes.
#:
#: It is 16 MiB plus 64 KiB, the Server's default maximum Value size plus
#: headroom for the envelope. Change it with
#: ``PayloadEncryptionOptions.max_envelope_bytes``.
#:
#: .. versionadded:: 1.45.10
DEFAULT_MAX_PAYLOAD_ENVELOPE_BYTES = 16_842_752

#: The default largest decrypted or decompressed plaintext, 64 MiB.
#:
#: It also stops a gzip bomb. Change it with
#: ``PayloadEncryptionOptions.max_plaintext_bytes`` and
#: ``AesGcmPayloadCodec(max_decompressed_bytes=...)``.
#:
#: .. versionadded:: 1.45.10
DEFAULT_MAX_PAYLOAD_PLAINTEXT_BYTES = 67_108_864

_AES_GCM_KEY_BYTES = 32
_AES_GCM_NONCE_BYTES = 12
_AES_GCM_TAG_BYTES = 16
_MAX_KEY_ID_BYTES = 64
_MISSING_DOCUMENT = object()
_ENCRYPTION_EXTRA_HINT = (
    "AesGcmPayloadCodec requires the cryptography package; install it with "
    "pip install 'dex-python-sdk[encryption]'"
)


class PayloadErrorCategory(Enum):
    """Name why a payload could not be encrypted or decrypted.

    Every SDK, dexcli, and codec server reports the same categories, so a
    failure reads the same everywhere. The value is the category text of the
    ``dex/1`` format specification.

    .. versionadded:: 1.45.10

    Attributes:
        MALFORMED: The envelope, its base64 text, or its gzip data is invalid.
        UNSUPPORTED_MARKER: The object encoding starts with ``dex/`` but is
            not ``dex/1``. A newer SDK wrote it.
        UNSUPPORTED_CIPHER: The configured codec does not support the
            envelope's cipher.
        UNSUPPORTED_COMPRESSION: The envelope names an unknown compression.
        UNKNOWN_KEY: The key provider does not know, or refuses, the key ID.
        AUTHENTICATION_FAILED: AES-GCM authentication failed. The ciphertext,
            key ID, or header was changed, or the key is wrong.
        LIMIT_EXCEEDED: A size limit was exceeded.
        INVALID_PLAINTEXT: The decrypted bytes are not a Value that Dex
            encrypts, or an encoder was asked to encrypt such a Value.
        CODEC_FAILED: A custom codec raised an exception without a category
            of the ``dex/1`` format. The SDK reports it without its text.
            It is not one of the format's categories; it never describes an
            envelope.
    """

    MALFORMED = "malformed"
    UNSUPPORTED_MARKER = "unsupported_marker"
    UNSUPPORTED_CIPHER = "unsupported_cipher"
    UNSUPPORTED_COMPRESSION = "unsupported_compression"
    UNKNOWN_KEY = "unknown_key"
    AUTHENTICATION_FAILED = "authentication_failed"
    LIMIT_EXCEEDED = "limit_exceeded"
    INVALID_PLAINTEXT = "invalid_plaintext"
    CODEC_FAILED = "codec_failed"


class PayloadEncryptionError(ValueMappingError):
    """Report that a payload could not be encrypted or decrypted.

    The message names only the operation, the category, and the key ID. It
    never contains payload bytes, plaintext, or key bytes. Decoding never
    falls back to plaintext: an envelope that fails to decode is always an
    error.

    It is a ``ValueMappingError``, so code that already handles value mapping
    failures also handles it. A Worker reports it as a failed Step or RPC and
    keeps its text in plaintext, because the text holds no payload content.

    .. versionadded:: 1.45.10

    Examples:
        >>> try:
        ...     values = client.get_attributes("order-42", ORDER_STATUS)
        ... except PayloadEncryptionError as error:
        ...     logger.error("cannot decrypt %s with key %s", error.category, error.key_id)

    Attributes:
        category: Why the payload failed.
        key_id: The key ID named by the envelope, or ``""`` when unknown.
    """

    def __init__(
        self,
        operation: str,
        category: PayloadErrorCategory,
        detail: str,
        key_id: str = "",
    ) -> None:
        """Create the error.

        Args:
            operation: ``"encode"`` or ``"decode"``.
            category: Why the payload failed.
            detail: A short reason without payload, plaintext, or key bytes.
            key_id: The key ID named by the envelope, or ``""``.
        """
        key_text = f" (key id {key_id})" if key_id else ""
        super().__init__(
            operation,
            f"payload {category.value}{key_text}: {detail}",
        )
        self.category = category
        self.key_id = key_id


@dataclass(frozen=True)
class PayloadContext:
    """Identify the Flow that stores the payloads of one codec call.

    A key provider uses it to choose keys, for example one key per tenant.
    The SDK fills it with the Flow that stores the Values:

    * A SubFlow's input and initial Attributes use the child Flow's type and
      its real Flow ID, ``SubFlow:{parentFlowID}-{stepExecutionID}-{index}``.
      A tenant-aware key provider must handle that prefix.
    * ``flow_type`` is ``""`` when the SDK does not know the Flow type. That
      happens in Client calls that take only a Flow ID, such as
      ``wait_for_flow``, ``get_attributes``, ``stop_flow``, ``time_travel``,
      and ``wait_for_attribute_match``, and when a parent Step reads a SubFlow
      result. Choose keys by ``flow_id`` when that matters.

    The context is not part of the ciphertext's authenticated data. Clone,
    fork, and reset copy Values into another run or Flow ID, and they still
    decrypt there.

    .. versionadded:: 1.45.10

    Examples:
        >>> context = PayloadContext(flow_type="OrderFlow", flow_id="order-42")
        >>> key_id = key_provider.current_key_id(context)

    Attributes:
        flow_type: The Flow type that stores the Values, or ``""`` when unknown.
        flow_id: The Flow ID that stores the Values.
    """

    flow_type: str
    flow_id: str


class PayloadCodec(Protocol):
    """Encrypt serialized Values into envelopes and back, synchronously.

    Implement it to plug in your own encryption, for example a KMS. Most
    applications use :class:`AesGcmPayloadCodec` instead. The SDK calls the
    codec in batches, so one call can serve many Values. ``Client`` and
    ``Worker`` accept only this synchronous interface. ``AsyncClient`` and
    ``AsyncWorker`` accept it too and call it through ``asyncio.to_thread``.

    A custom codec sets ``cipher`` to ``PAYLOAD_CIPHER_CUSTOM`` and keeps its
    parameters in ``metadata``. It must authenticate every metadata entry
    that affects decoding, or reject metadata it does not authenticate. The
    SDK checks the marker, the size limits, the key ID, and the metadata
    limits before ``decode``, and checks each returned plaintext afterwards.

    The SDK never shows a custom codec's exception text. It replaces every
    exception from ``encode`` or ``decode``, including a
    ``PayloadEncryptionError``, with its own ``PayloadEncryptionError``. That
    error keeps only a defined category, or ``codec_failed`` otherwise, and
    the key ID when it names an envelope of the batch. The original
    exception is neither chained nor logged, so key material or KMS details
    in it never reach errors, logs, or Worker failure details. Log what you
    need inside the codec.

    ``decode`` may declare an extra keyword-only parameter with a default,
    ``max_plaintext_bytes: int | None = None``, which keeps it a
    ``PayloadCodec`` for type checkers. The SDK then passes
    ``PayloadEncryptionOptions.max_plaintext_bytes``, and a codec that
    decompresses must stop as soon as its output exceeds that limit. The SDK
    checks each returned plaintext against the same limit in any case.

    .. versionadded:: 1.45.10

    Examples:
        >>> from dex.dexpb import dex_pb2 as pb
        >>> class KmsPayloadCodec:
        ...     def encode(self, context, plaintexts):
        ...         return [
        ...             pb.PayloadEnvelope(
        ...                 cipher=pb.PAYLOAD_CIPHER_CUSTOM,
        ...                 key_id="kms-1",
        ...                 data=kms.encrypt(plaintext, context.flow_id),
        ...             )
        ...             for plaintext in plaintexts
        ...         ]
        ...
        ...     def decode(self, context, envelopes, *, max_plaintext_bytes=None):
        ...         return [
        ...             kms.decrypt(envelope.data, max_output=max_plaintext_bytes)
        ...             for envelope in envelopes
        ...         ]
    """

    def encode(
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> Sequence[pb.PayloadEnvelope]:
        """Encrypt serialized Values.

        Args:
            context: The Flow that stores the Values.
            plaintexts: Serialized ``dex.Value`` messages.

        Returns:
            Exactly one envelope per plaintext, in order.

        Raises:
            Exception: Any failure fails the whole batch and the SDK call.
        """
        ...

    def decode(
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> Sequence[bytes]:
        """Decrypt envelopes back into serialized Values.

        To receive the SDK's plaintext limit, declare an extra keyword-only
        parameter ``max_plaintext_bytes: int | None = None`` and stop
        decompressing once the output exceeds it. See :class:`PayloadCodec`.

        Args:
            context: The Flow that stores the Values.
            envelopes: Parsed envelopes that passed the SDK's checks.

        Returns:
            Exactly one plaintext per envelope, in order.

        Raises:
            PayloadEncryptionError: When an envelope cannot be decrypted.
                Any other exception also fails the whole batch. The SDK
                replaces either with its own error without the text.
        """
        ...


class AsyncPayloadCodec(Protocol):
    """Encrypt serialized Values into envelopes and back, asynchronously.

    Implement it when encryption needs network calls, such as a KMS, and the
    application uses ``AsyncClient`` and ``AsyncWorker``. The synchronous
    ``Client`` and ``Worker`` reject it. The rules of :class:`PayloadCodec`
    apply, including the replaced exceptions and the optional
    ``max_plaintext_bytes`` keyword of ``decode``.

    .. versionadded:: 1.45.10

    Examples:
        >>> class AsyncKmsPayloadCodec:
        ...     async def encode(self, context, plaintexts):
        ...         keys = await kms.data_keys(context.flow_id, len(plaintexts))
        ...         return [seal(key, plaintext) for key, plaintext in zip(keys, plaintexts)]
        ...
        ...     async def decode(self, context, envelopes):
        ...         return [await kms.open(envelope) for envelope in envelopes]
    """

    async def encode(
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> Sequence[pb.PayloadEnvelope]:
        """Encrypt serialized Values.

        Args:
            context: The Flow that stores the Values.
            plaintexts: Serialized ``dex.Value`` messages.

        Returns:
            Exactly one envelope per plaintext, in order.

        Raises:
            Exception: Any failure fails the whole batch and the SDK call.
        """
        ...

    async def decode(
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> Sequence[bytes]:
        """Decrypt envelopes back into serialized Values.

        To receive the SDK's plaintext limit, declare an extra keyword-only
        parameter ``max_plaintext_bytes: int | None = None`` and stop
        decompressing once the output exceeds it. See :class:`PayloadCodec`.

        Args:
            context: The Flow that stores the Values.
            envelopes: Parsed envelopes that passed the SDK's checks.

        Returns:
            Exactly one plaintext per envelope, in order.

        Raises:
            PayloadEncryptionError: When an envelope cannot be decrypted.
                Any other exception also fails the whole batch. The SDK
                replaces either with its own error without the text.
        """
        ...


class PayloadEncryptionKeyProvider(Protocol):
    """Supply the AES-256 keys of :class:`AesGcmPayloadCodec`.

    ``current_key_id`` chooses the key for new encryptions. ``key`` returns
    the key for an envelope's key ID. Keep old keys available after rotation
    so that older envelopes stay decodable. Keys stay in memory; never log
    them. :class:`FilePayloadEncryptionKeyProvider` reads a key file.

    A multi-tenant provider should refuse, in ``key``, a key ID that does not
    belong to the context's tenant. The Server could otherwise move another
    tenant's ciphertext into this tenant's Flow, and it would decrypt.

    .. versionadded:: 1.45.10

    Examples:
        >>> class TenantKeyProvider:
        ...     def current_key_id(self, context):
        ...         return f"{tenant_of(context.flow_id)}-2026"
        ...
        ...     def key(self, context, key_id):
        ...         if not key_id.startswith(tenant_of(context.flow_id) + "-"):
        ...             raise KeyError(key_id)
        ...         return vault.read_key(key_id)
    """

    def current_key_id(self, context: PayloadContext) -> str:
        """Return the key ID for new encryptions in ``context``.

        Args:
            context: The Flow that stores the Values.

        Returns:
            A key ID of 1 to 64 printable ASCII bytes without spaces. It is
            visible in clear in every envelope, so it must not hold secrets.
        """
        ...

    def key(self, context: PayloadContext, key_id: str) -> bytes:
        """Return the 32-byte key for ``key_id``.

        Args:
            context: The Flow that stores the Values.
            key_id: The key ID from an envelope or from ``current_key_id``.

        Returns:
            The 32-byte AES-256 key.

        Raises:
            KeyError: When the key ID is unknown or not allowed for
                ``context``. The codec reports ``unknown_key`` for this and
                for any other exception, and drops the exception and its text
                so that nothing from the provider reaches errors or logs.
        """
        ...


class PayloadCompression(Enum):
    """Choose whether :class:`AesGcmPayloadCodec` compresses before encrypting.

    Compression makes large, repetitive Values smaller. Ciphertext length
    then reveals how well the content compresses. Do not compress Values that
    mix attacker-controlled content with secrets; an attacker who sees the
    lengths can learn the secrets, as in the CRIME and BREACH attacks.

    .. versionadded:: 1.45.10

    Attributes:
        NONE: Encrypt the serialized Value as is. The default.
        GZIP: Compress the serialized Value with gzip, then encrypt it.
    """

    NONE = "none"
    GZIP = "gzip"


class AesGcmPayloadCodec:
    """Encrypt payloads with AES-256-GCM and an optional gzip step.

    It is the built-in :class:`PayloadCodec`. Each encryption uses the key
    that ``key_provider.current_key_id`` names and a fresh random 12-byte
    nonce. Decryption uses the key that the envelope's key ID names, so keys
    can rotate while old envelopes stay readable. Encrypt at most 2**32
    Values with one key, and rotate keys regularly.

    It needs the ``cryptography`` package. Install the SDK with the
    ``encryption`` extra: ``pip install 'dex-python-sdk[encryption]'``.

    .. versionadded:: 1.45.10

    Examples:
        >>> key_provider = FilePayloadEncryptionKeyProvider("/etc/dex/payload-keys.json")
        >>> encryption = PayloadEncryptionOptions(AesGcmPayloadCodec(key_provider))
        >>> client = Client(registry, cache, ClientOptions(payload_encryption=encryption))
    """

    def __init__(
        self,
        key_provider: PayloadEncryptionKeyProvider,
        *,
        compression: PayloadCompression = PayloadCompression.NONE,
        max_decompressed_bytes: int = DEFAULT_MAX_PAYLOAD_PLAINTEXT_BYTES,
    ) -> None:
        """Create the codec.

        Args:
            key_provider: Supplies the current key ID and the keys.
            compression: Whether to gzip each Value before encrypting it.
                Defaults to ``PayloadCompression.NONE``. See
                :class:`PayloadCompression` for the length-leak risk.
            max_decompressed_bytes: The largest plaintext that decryption and
                decompression may produce, in bytes. Decompression stops as
                soon as it exceeds the limit. Defaults to 64 MiB.

        Raises:
            ImportError: If the ``cryptography`` package is not installed.
            ValueError: If ``max_decompressed_bytes`` is not positive.
            TypeError: If ``compression`` is not a ``PayloadCompression``.
        """
        self._aes_gcm_type = _load_aes_gcm()
        if not isinstance(compression, PayloadCompression):
            raise TypeError("compression must be a PayloadCompression")
        if max_decompressed_bytes <= 0:
            raise ValueError("max_decompressed_bytes must be positive")
        self._key_provider = key_provider
        self._compression = compression
        self._max_decompressed_bytes = max_decompressed_bytes
        # None checks no data size beyond the SDK's envelope limit.
        self._max_data_bytes: int | None = None

    def encode(
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> list[pb.PayloadEnvelope]:
        """Encrypt serialized Values with the current key.

        Args:
            context: The Flow that stores the Values.
            plaintexts: Serialized ``dex.Value`` messages.

        Returns:
            One ``PAYLOAD_CIPHER_AES256_GCM`` envelope per plaintext, in order.

        Raises:
            PayloadEncryptionError: If the current key ID is invalid or
                unknown, or its key is not 32 bytes.
        """
        if not plaintexts:
            return []
        key_id: object = None
        try:
            key_id = self._key_provider.current_key_id(context)
        except Exception:
            key_id = None
        if key_id is None:
            # Raised outside the handler so no provider text survives as context.
            raise PayloadEncryptionError(
                "encode",
                PayloadErrorCategory.UNKNOWN_KEY,
                "the key provider failed to choose the current key id",
            )
        if not isinstance(key_id, str) or not is_valid_payload_key_id(key_id):
            raise PayloadEncryptionError(
                "encode",
                PayloadErrorCategory.MALFORMED,
                "current key id must be 1 to 64 printable ASCII bytes",
            )
        aead = self._aead(context, key_id, "encode")
        compression = (
            pb.PAYLOAD_COMPRESSION_GZIP
            if self._compression is PayloadCompression.GZIP
            else pb.PAYLOAD_COMPRESSION_NONE
        )
        aad = payload_envelope_aad(pb.PAYLOAD_CIPHER_AES256_GCM, compression, key_id)
        envelopes: list[pb.PayloadEnvelope] = []
        for plaintext in plaintexts:
            sealed = (
                _gzip_compress(plaintext)
                if compression == pb.PAYLOAD_COMPRESSION_GZIP
                else bytes(plaintext)
            )
            nonce = os.urandom(_AES_GCM_NONCE_BYTES)
            envelopes.append(
                pb.PayloadEnvelope(
                    cipher=pb.PAYLOAD_CIPHER_AES256_GCM,
                    key_id=key_id,
                    data=nonce + aead.encrypt(nonce, sealed, aad),
                    compression=compression,
                )
            )
        return envelopes

    def decode(
        self,
        context: PayloadContext,
        envelopes: Sequence[pb.PayloadEnvelope],
        *,
        max_plaintext_bytes: int | None = None,
    ) -> list[bytes]:
        """Decrypt envelopes with the keys their key IDs name.

        Decompression stops at the smaller of ``max_decompressed_bytes`` and
        ``max_plaintext_bytes``.

        Args:
            context: The Flow that stores the Values.
            envelopes: Parsed envelopes.
            max_plaintext_bytes: The SDK's plaintext limit for this call, or
                ``None`` to use only ``max_decompressed_bytes``. The SDK
                passes ``PayloadEncryptionOptions.max_plaintext_bytes``.

        Returns:
            One serialized ``dex.Value`` per envelope, in order.

        Raises:
            PayloadEncryptionError: With ``unsupported_cipher`` for another
                cipher, ``malformed`` for metadata, short data, or invalid
                gzip, ``unsupported_compression``, ``unknown_key``,
                ``authentication_failed``, or ``limit_exceeded``.
        """
        limit = self._max_decompressed_bytes
        if max_plaintext_bytes is not None:
            limit = min(limit, max_plaintext_bytes)
        aeads: dict[str, Any] = {}
        plaintexts: list[bytes] = []
        for envelope in envelopes:
            plaintexts.append(self._decode_one(context, envelope, aeads, limit))
        return plaintexts

    def _decode_one(
        self,
        context: PayloadContext,
        envelope: pb.PayloadEnvelope,
        aeads: dict[str, Any],
        max_plaintext_bytes: int,
    ) -> bytes:
        key_id = envelope.key_id
        if envelope.cipher != pb.PAYLOAD_CIPHER_AES256_GCM:
            raise PayloadEncryptionError(
                "decode",
                PayloadErrorCategory.UNSUPPORTED_CIPHER,
                f"cipher {int(envelope.cipher)} is not AES-256-GCM",
                key_id,
            )
        if envelope.metadata:
            raise PayloadEncryptionError(
                "decode",
                PayloadErrorCategory.MALFORMED,
                "AES-256-GCM envelopes carry no metadata",
                key_id,
            )
        if envelope.compression not in (
            pb.PAYLOAD_COMPRESSION_NONE,
            pb.PAYLOAD_COMPRESSION_GZIP,
        ):
            raise PayloadEncryptionError(
                "decode",
                PayloadErrorCategory.UNSUPPORTED_COMPRESSION,
                f"compression {int(envelope.compression)} is unknown",
                key_id,
            )
        aead = aeads.get(key_id)
        if aead is None:
            aead = self._aead(context, key_id, "decode")
            aeads[key_id] = aead
        data = envelope.data
        if len(data) < _AES_GCM_NONCE_BYTES + _AES_GCM_TAG_BYTES:
            raise PayloadEncryptionError(
                "decode",
                PayloadErrorCategory.MALFORMED,
                "data is shorter than nonce and tag",
                key_id,
            )
        if self._max_data_bytes is not None and len(data) > self._max_data_bytes:
            raise PayloadEncryptionError(
                "decode",
                PayloadErrorCategory.LIMIT_EXCEEDED,
                "data exceeds the size limit",
                key_id,
            )
        aad = payload_envelope_aad(envelope.cipher, envelope.compression, key_id)
        opened: bytes | None = None
        try:
            opened = aead.decrypt(
                data[:_AES_GCM_NONCE_BYTES], data[_AES_GCM_NONCE_BYTES:], aad
            )
        except Exception:
            opened = None
        if opened is None:
            raise PayloadEncryptionError(
                "decode",
                PayloadErrorCategory.AUTHENTICATION_FAILED,
                "AES-GCM authentication failed",
                key_id,
            )
        if envelope.compression == pb.PAYLOAD_COMPRESSION_GZIP:
            return _gzip_decompress_bounded(opened, max_plaintext_bytes, key_id)
        if len(opened) > max_plaintext_bytes:
            raise PayloadEncryptionError(
                "decode",
                PayloadErrorCategory.LIMIT_EXCEEDED,
                "plaintext exceeds the size limit",
                key_id,
            )
        return bytes(opened)

    def _aead(self, context: PayloadContext, key_id: str, operation: str) -> Any:
        key: object = None
        try:
            key = self._key_provider.key(context, key_id)
        except Exception:
            key = None
        if key is None:
            # Raised outside the handler so no provider text survives as context.
            raise PayloadEncryptionError(
                operation,
                PayloadErrorCategory.UNKNOWN_KEY,
                "the key provider does not know or refuses this key id",
                key_id,
            )
        if not isinstance(key, (bytes, bytearray)) or len(key) != _AES_GCM_KEY_BYTES:
            raise PayloadEncryptionError(
                operation,
                PayloadErrorCategory.UNKNOWN_KEY,
                "the key provider returned a key that is not 32 bytes",
                key_id,
            )
        return self._aes_gcm_type(bytes(key))


class FilePayloadEncryptionKeyProvider:
    """Read payload encryption keys from a JSON key file.

    The file uses the format that dexcli and every Dex SDK share::

        {
          "current_key_id": "k2",
          "keys": {
            "k1": "<standard base64 of 32 bytes>",
            "k2": "<standard base64 of 32 bytes>"
          }
        }

    The provider reads and validates the file once, when it is constructed.
    It logs a warning when other users can read the file; keep it mode
    ``0600``. ``dexcli codec-server keygen`` writes such a file. To rotate,
    add a new key, point ``current_key_id`` at it, keep the old keys, and
    restart. The provider ignores the context, so Values cloned into another
    Flow still decrypt.

    .. versionadded:: 1.45.10

    Examples:
        >>> key_provider = FilePayloadEncryptionKeyProvider("/etc/dex/payload-keys.json")
        >>> codec = AesGcmPayloadCodec(key_provider)
    """

    def __init__(self, path: str | os.PathLike[str]) -> None:
        """Read and validate the key file.

        Args:
            path: The key file path.

        Raises:
            OSError: If the file cannot be read.
            ValueError: If the file is not valid key file JSON, a key ID is
                invalid, a key is not standard padded base64 of 32 bytes, or
                ``current_key_id`` is missing from ``keys``. The message never
                contains key bytes.
        """
        file_path = Path(path)
        _warn_if_readable_by_others(file_path)
        content = file_path.read_bytes()
        document: object = _MISSING_DOCUMENT
        try:
            document = json.loads(content)
        except ValueError:
            document = _MISSING_DOCUMENT
        # Raised outside the handler: a JSON error keeps the document, keys included.
        if document is _MISSING_DOCUMENT:
            raise ValueError(f"payload key file {file_path} is not valid JSON")
        if not isinstance(document, dict):
            raise ValueError(f"payload key file {file_path} must hold a JSON object")
        current_key_id = document.get("current_key_id")
        keys = document.get("keys")
        if not isinstance(keys, dict) or not keys:
            raise ValueError(
                f"payload key file {file_path} needs a non-empty keys object"
            )
        loaded: dict[str, bytes] = {}
        for key_id, encoded in keys.items():
            if not is_valid_payload_key_id(key_id):
                raise ValueError(
                    f"payload key file {file_path} has an invalid key id; key ids "
                    "are 1 to 64 printable ASCII bytes without spaces"
                )
            loaded[key_id] = _decode_key_file_key(file_path, key_id, encoded)
        if not isinstance(current_key_id, str) or current_key_id not in loaded:
            raise ValueError(
                f"payload key file {file_path} current_key_id must name a key in keys"
            )
        self._current_key_id = current_key_id
        self._keys = loaded

    def current_key_id(self, context: PayloadContext) -> str:
        """Return the file's ``current_key_id``.

        Args:
            context: Ignored.

        Returns:
            The key ID for new encryptions.
        """
        return self._current_key_id

    def key(self, context: PayloadContext, key_id: str) -> bytes:
        """Return the key for ``key_id``.

        Args:
            context: Ignored, so clones into another Flow ID still decrypt.
            key_id: The key ID to look up.

        Returns:
            The 32-byte key.

        Raises:
            KeyError: If the file has no such key ID.
        """
        try:
            return self._keys[key_id]
        except KeyError:
            raise KeyError(key_id) from None

    def __repr__(self) -> str:
        """Describe the provider without key bytes.

        Returns:
            The class name, the current key ID, and the key IDs.
        """
        return (
            f"FilePayloadEncryptionKeyProvider(current_key_id={self._current_key_id!r}, "
            f"key_ids={sorted(self._keys)!r})"
        )


@dataclass(frozen=True)
class PayloadEncryptionOptions:
    """Turn on client-side payload encryption for a Client or Worker.

    Pass it as ``ClientOptions.payload_encryption`` or
    ``WorkerOptions.payload_encryption``. Every Client and Worker that reads
    a Flow must use the same keys. Roll out in two steps: first give every
    Worker and Client the codec, which lets them read encrypted Values, then
    turn encryption on where Values are written. Decoding works on every
    Value marked ``dex/1`` and passes every other Value through, so encrypted
    and plaintext Flows can share one Worker.

    The SDK encrypts string, object, int, double, and bool Values. It never
    encrypts null, which deletes an Attribute, or Server blob references. It
    also leaves these Values in plaintext, because the Server reads them:

    * Attribute writes with an index, so search keeps working.
    * Attributes and AttributeMaps declared with ``plaintext=True``.
    * ``wait_for_attribute_match`` operands and Action condition values.

    An encrypted Attribute that syncs to an Attribute Store writes its
    ciphertext, so its column must be ``bytea``. The Server logs and skips a
    value for any other column type, and the writer gets no error. Read the
    column and decrypt it with the same codec, or declare the Attribute
    ``plaintext=True``.

    ``wait_for_attribute_match`` and Actions with a condition need a plaintext
    Attribute. With encryption on, the SDK rejects them when the Attribute
    would be encrypted, before anything reaches the Server. Index the
    Attribute or declare it ``plaintext=True``.

    Error text and reasons are encrypted into a ``dex-encoded:`` text form:
    Worker error detail and stack trace, ``force_fail`` reasons, and the
    reasons of ``stop_flow``, ``time_travel``, and ``clone_fork_flow``. Flow
    types, Flow IDs, Attribute keys, Channel names, and other identifiers
    stay in plaintext; keep secrets out of them.

    Java and Rust SDKs cannot read encrypted Flows yet.

    .. versionadded:: 1.45.10

    Examples:
        >>> key_provider = FilePayloadEncryptionKeyProvider("/etc/dex/payload-keys.json")
        >>> encryption = PayloadEncryptionOptions(AesGcmPayloadCodec(key_provider))
        >>> worker = Worker(registry, cache, WorkerOptions(payload_encryption=encryption))
        >>> client = Client(registry, cache, ClientOptions(payload_encryption=encryption))

    Attributes:
        codec: The one codec of this Client or Worker. ``Client`` and
            ``Worker`` need a :class:`PayloadCodec`; ``AsyncClient`` and
            ``AsyncWorker`` also accept an :class:`AsyncPayloadCodec`.
        encrypt_string_values: Whether to encrypt string Values. Defaults to
            ``True``. ``False`` sends them in plaintext.
        encrypt_primitive_values: Whether to encrypt int, double, and bool
            Values. Defaults to ``True``. ``False`` sends them in plaintext.
        encrypt_error_text: Whether to encrypt error text and reasons.
            Defaults to ``True``. With ``False``, text that starts with
            ``dex-encoded:`` is still encrypted, so readers never mistake it
            for an envelope.
        max_envelope_bytes: The largest serialized envelope to read or write,
            in bytes. Defaults to 16 MiB plus 64 KiB. Raise it only for a
            Server configured with a larger maximum Value size.
        max_plaintext_bytes: The largest plaintext to read or write, in
            bytes. Defaults to 64 MiB.
    """

    codec: PayloadCodec | AsyncPayloadCodec
    encrypt_string_values: bool = True
    encrypt_primitive_values: bool = True
    encrypt_error_text: bool = True
    max_envelope_bytes: int = DEFAULT_MAX_PAYLOAD_ENVELOPE_BYTES
    max_plaintext_bytes: int = DEFAULT_MAX_PAYLOAD_PLAINTEXT_BYTES

    def __post_init__(self) -> None:
        has_codec_methods = False
        control_flow: BaseException | None = None
        try:
            has_codec_methods = callable(
                getattr(self.codec, "encode", None)
            ) and callable(getattr(self.codec, "decode", None))
        except BaseException as error:
            control_flow = _fresh_control_flow_exception(error)
        # Raised outside the handler, so no codec exception text is chained.
        if control_flow is not None:
            raise control_flow
        if not has_codec_methods:
            raise TypeError("payload codec must define encode and decode")
        if self.max_envelope_bytes <= 0:
            raise ValueError("max_envelope_bytes must be positive")
        if self.max_plaintext_bytes <= 0:
            raise ValueError("max_plaintext_bytes must be positive")


def is_valid_payload_key_id(key_id: object) -> bool:
    """Return whether ``key_id`` is a valid payload key ID.

    A key ID is 1 to 64 bytes, each printable ASCII from ``0x21`` to ``0x7e``.
    Spaces are not allowed.

    .. versionadded:: 1.45.10

    Examples:
        >>> is_valid_payload_key_id("k2")
        True
        >>> is_valid_payload_key_id("key 2")
        False

    Args:
        key_id: The candidate key ID.

    Returns:
        ``True`` when ``key_id`` is a string that follows the rule.
    """
    if not isinstance(key_id, str) or not 1 <= len(key_id) <= _MAX_KEY_ID_BYTES:
        return False
    return all(0x21 <= ord(character) <= 0x7E for character in key_id)


def payload_envelope_aad(cipher: int, compression: int, key_id: str) -> bytes:
    """Return the additional authenticated data of an AES-256-GCM envelope.

    The AAD is ``"dex/1" 0x00 <cipher> 0x00 <compression> 0x00 <key_id>``,
    with the enum numbers as ASCII decimal digits. Use it in a custom codec
    that interoperates with the built-in one.

    .. versionadded:: 1.45.10

    Examples:
        >>> payload_envelope_aad(1, 0, "k1")
        b'dex/1\\x001\\x000\\x00k1'

    Args:
        cipher: The ``PayloadCipher`` number.
        compression: The ``PayloadCompression`` number.
        key_id: The envelope's key ID.

    Returns:
        The AAD bytes.
    """
    return b"\x00".join(
        (
            b"dex/1",
            str(int(cipher)).encode("ascii"),
            str(int(compression)).encode("ascii"),
            key_id.encode("ascii"),
        )
    )


_CONTROL_FLOW_EXCEPTION_TYPES: tuple[type[BaseException], ...] = (
    asyncio.CancelledError,
    KeyboardInterrupt,
    SystemExit,
    GeneratorExit,
)


def _fresh_control_flow_exception(error: BaseException) -> BaseException | None:
    """Return a new, argument-free control-flow exception of the same kind, or ``None``."""
    for control_flow_type in _CONTROL_FLOW_EXCEPTION_TYPES:
        if issubclass(type(error), control_flow_type):
            return control_flow_type()
    return None


def _load_aes_gcm() -> Any:
    try:
        from cryptography.hazmat.primitives.ciphers.aead import AESGCM
    except ImportError as error:
        raise ImportError(_ENCRYPTION_EXTRA_HINT) from error
    return AESGCM


def _gzip_compress(plaintext: bytes) -> bytes:
    compressor = zlib.compressobj(wbits=16 + zlib.MAX_WBITS)
    return compressor.compress(plaintext) + compressor.flush()


def _gzip_decompress_bounded(data: bytes, max_bytes: int, key_id: str) -> bytes:
    """Return the gzip output, stopping as soon as it exceeds ``max_bytes``."""
    output = bytearray()
    remaining = data
    while True:
        decompressor = zlib.decompressobj(wbits=16 + zlib.MAX_WBITS)
        chunk: bytes | None = None
        try:
            chunk = decompressor.decompress(remaining, max_bytes + 1 - len(output))
        except zlib.error:
            chunk = None
        if chunk is None:
            raise PayloadEncryptionError(
                "decode", PayloadErrorCategory.MALFORMED, "invalid gzip data", key_id
            )
        output += chunk
        if len(output) > max_bytes:
            raise PayloadEncryptionError(
                "decode",
                PayloadErrorCategory.LIMIT_EXCEEDED,
                "decompressed plaintext exceeds the size limit",
                key_id,
            )
        if not decompressor.eof:
            raise PayloadEncryptionError(
                "decode", PayloadErrorCategory.MALFORMED, "truncated gzip data", key_id
            )
        # Concatenated gzip members decode as one stream, as in Go and Node.
        remaining = decompressor.unused_data
        if not remaining:
            return bytes(output)


def _decode_key_file_key(path: Path, key_id: str, encoded: object) -> bytes:
    invalid = ValueError(
        f"payload key file {path} key {key_id} must be standard padded base64 "
        "of 32 bytes"
    )
    if not isinstance(encoded, str):
        raise invalid
    key: bytes | None = None
    try:
        key = base64.b64decode(encoded, validate=True)
    except (binascii.Error, ValueError):
        key = None
    if key is None:
        raise invalid
    if base64.b64encode(key).decode("ascii") != encoded or len(key) != 32:
        raise invalid
    return key


def _warn_if_readable_by_others(path: Path) -> None:
    try:
        mode = path.stat().st_mode
    except OSError:
        return
    if os.name == "posix" and mode & (stat.S_IRWXG | stat.S_IRWXO):
        _LOGGER.warning(
            "payload key file %s is accessible by other users; restrict it to mode 0600",
            path,
        )
