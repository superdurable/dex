# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Loads the payload encryption keys. Replace this module's factory to use a KMS."""

from __future__ import annotations

import base64
import binascii
import json

from dex import (
    AesGcmPayloadCodec,
    FilePayloadEncryptionKeyProvider,
    PayloadContext,
    PayloadEncryptionKeyProvider,
    is_valid_payload_key_id,
)

from dex_codec_server.config import CodecServerConfig, CodecServerConfigurationError

PAYLOAD_KEY_BYTES = 32


def create_payload_encryption_key_provider(
    config: CodecServerConfig,
) -> PayloadEncryptionKeyProvider:
    """Return the key provider for the configured key source.

    KMS: return a KMS-backed ``PayloadEncryptionKeyProvider`` here. See README.md.

    Args:
        config: The validated configuration.

    Returns:
        A provider for the key file or for ``DEX_CODEC_SERVER_KEYS_JSON``.

    Raises:
        OSError: When the key file cannot be read.
        ValueError: When the keys break the key file rules. The message holds no key bytes.
    """
    if config.key_file_path:
        return FilePayloadEncryptionKeyProvider(config.key_file_path)
    return KeyFileJsonPayloadEncryptionKeyProvider(config.keys_json)


def create_aes_gcm_payload_codec(
    key_provider: PayloadEncryptionKeyProvider, max_plaintext_bytes: int
) -> AesGcmPayloadCodec:
    """Return the SDK's AES-256-GCM codec, which reads exactly what Workers write.

    Args:
        key_provider: Supplies the keys.
        max_plaintext_bytes: Stops gzip decompression as soon as output exceeds it.

    Returns:
        The codec. It never compresses, so ciphertext length does not leak compressibility.
    """
    return AesGcmPayloadCodec(key_provider, max_decompressed_bytes=max_plaintext_bytes)


class KeyFileJsonPayloadEncryptionKeyProvider:
    """Read the key file format from memory, such as an environment variable.

    The keys stay in memory and are never written to disk.
    """

    def __init__(self, key_file_json: str) -> None:
        """Parse and validate the key file JSON.

        Args:
            key_file_json: The key file document.

        Raises:
            CodecServerConfigurationError: When the document breaks the key file
                rules. The message names key IDs but never key bytes.
        """
        try:
            document = json.loads(key_file_json)
        except ValueError:
            raise CodecServerConfigurationError(
                "DEX_CODEC_SERVER_KEYS_JSON is not valid key file JSON"
            ) from None
        keys = document.get("keys") if isinstance(document, dict) else None
        if not isinstance(keys, dict) or not keys:
            raise CodecServerConfigurationError(
                "DEX_CODEC_SERVER_KEYS_JSON needs a non-empty keys object"
            )
        loaded: dict[str, bytes] = {}
        for key_id, encoded in keys.items():
            if not is_valid_payload_key_id(key_id):
                raise CodecServerConfigurationError(
                    "DEX_CODEC_SERVER_KEYS_JSON has an invalid key id; key ids are 1 to 64 "
                    "printable ASCII bytes without spaces"
                )
            loaded[key_id] = _decode_key(key_id, encoded)
        current_key_id = document.get("current_key_id")
        if not isinstance(current_key_id, str) or current_key_id not in loaded:
            raise CodecServerConfigurationError(
                "DEX_CODEC_SERVER_KEYS_JSON current_key_id must name a key in keys"
            )
        self._current_key_id = current_key_id
        self._keys = loaded

    def current_key_id(self, context: PayloadContext) -> str:
        """Return ``current_key_id`` for every context.

        Args:
            context: Ignored.

        Returns:
            The key ID for new encryptions.
        """
        return self._current_key_id

    def key(self, context: PayloadContext, key_id: str) -> bytes:
        """Return the key for ``key_id``.

        Args:
            context: Ignored, so Values cloned into another Flow still decrypt.
            key_id: The key ID to look up.

        Returns:
            The 32-byte key.

        Raises:
            KeyError: When the key ID is unknown.
        """
        try:
            return self._keys[key_id]
        except KeyError:
            raise KeyError(key_id) from None

    def __repr__(self) -> str:
        """Describe the provider without key bytes."""
        return (
            f"KeyFileJsonPayloadEncryptionKeyProvider(current_key_id={self._current_key_id!r}, "
            f"key_ids={sorted(self._keys)!r})"
        )


def _decode_key(key_id: str, encoded: object) -> bytes:
    problem = (
        f"DEX_CODEC_SERVER_KEYS_JSON key {key_id!r} is not 32 bytes of standard padded base64"
    )
    if not isinstance(encoded, str):
        raise CodecServerConfigurationError(problem)
    try:
        key = base64.b64decode(encoded, validate=True)
    except (binascii.Error, ValueError):
        raise CodecServerConfigurationError(problem) from None
    if len(key) != PAYLOAD_KEY_BYTES or base64.b64encode(key).decode("ascii") != encoded:
        raise CodecServerConfigurationError(problem)
    return key
