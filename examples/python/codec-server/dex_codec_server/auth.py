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

"""Authenticates callers and picks their keys. Replace these hooks for your identity provider."""

from __future__ import annotations

import hashlib
import hmac
from dataclasses import dataclass
from typing import Protocol

from aiohttp import web
from dex import AsyncPayloadCodec, PayloadCodec

from dex_codec_server.config import (
    AUTH_MODE_BEARER_TOKEN,
    AUTH_MODE_TRUSTED_PROXY_HEADER,
    CodecServerConfig,
)

AnyPayloadCodec = PayloadCodec | AsyncPayloadCodec


class CallerUnauthenticatedError(Exception):
    """Credentials are missing or invalid. The server answers 401."""


class CallerForbiddenError(Exception):
    """The caller may not use payload keys. The server answers 403."""


@dataclass(frozen=True)
class CallerIdentity:
    """Who an authenticator proved the caller to be."""

    subject: str


class RequestAuthenticator(Protocol):
    """Authenticate every ``/encode`` and ``/decode`` request.

    Implement it for your identity provider, for example to verify an OIDC token.
    """

    async def authenticate_request(self, request: web.Request) -> CallerIdentity:
        """Return the caller, or raise ``CallerUnauthenticatedError``.

        It runs before the server reads the request body.
        """
        ...


class BearerTokenRequestAuthenticator:
    """Accept one shared secret in ``Authorization: Bearer <token>``."""

    def __init__(self, token: str) -> None:
        """Keep only the token's digest.

        Args:
            token: The shared secret.
        """
        self._token_digest = hashlib.sha256(token.encode()).digest()

    async def authenticate_request(self, request: web.Request) -> CallerIdentity:
        """Compare the presented token with the secret in constant time."""
        scheme, _, token = request.headers.get("Authorization", "").partition(" ")
        if scheme.lower() != "bearer" or not token:
            raise CallerUnauthenticatedError()
        # Fixed-size digests keep the comparison constant-time for any token length.
        presented_digest = hashlib.sha256(token.encode()).digest()
        if not hmac.compare_digest(presented_digest, self._token_digest):
            raise CallerUnauthenticatedError()
        return CallerIdentity(subject="bearer-token")


class TrustedProxyHeaderRequestAuthenticator:
    """Trust a user header set by an SSO proxy. Safe only when that proxy is the sole path in."""

    def __init__(self, header_name: str) -> None:
        """Remember the header name.

        Args:
            header_name: The header the proxy sets, such as ``X-Forwarded-Email``.
        """
        self._header_name = header_name

    async def authenticate_request(self, request: web.Request) -> CallerIdentity:
        """Return the header's single non-empty value as the caller."""
        values = request.headers.getall(self._header_name, [])
        if len(values) != 1 or not values[0].strip():
            raise CallerUnauthenticatedError()
        return CallerIdentity(subject=values[0].strip())


def create_request_authenticator(config: CodecServerConfig) -> RequestAuthenticator:
    """Return the authenticator for the configured auth mode.

    Raises:
        ValueError: For an unknown auth mode.
    """
    if config.auth_mode == AUTH_MODE_BEARER_TOKEN:
        return BearerTokenRequestAuthenticator(config.bearer_token)
    if config.auth_mode == AUTH_MODE_TRUSTED_PROXY_HEADER:
        return TrustedProxyHeaderRequestAuthenticator(config.trusted_identity_header)
    raise ValueError("unknown auth mode")


class CallerPayloadCodecSelector(Protocol):
    """Pick the codec, and so the keys, from the authenticated caller.

    Never pick it from the request ``context``: the browser controls that.
    """

    async def select_payload_codec(self, caller: CallerIdentity) -> AnyPayloadCodec:
        """Return the caller's codec, or raise ``CallerForbiddenError`` for 403."""
        ...


class SingleTenantPayloadCodecSelector:
    """Let every authenticated caller use one codec."""

    def __init__(self, codec: AnyPayloadCodec) -> None:
        """Remember the codec.

        Args:
            codec: The codec every caller uses.
        """
        self._codec = codec

    async def select_payload_codec(self, caller: CallerIdentity) -> AnyPayloadCodec:
        """Return the one codec for any caller."""
        return self._codec
