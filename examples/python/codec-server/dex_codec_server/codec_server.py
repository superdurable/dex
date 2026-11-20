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

"""Serves the codec server protocol of ``protos/payload-encryption.md`` section 11."""

from __future__ import annotations

import asyncio
import base64
import binascii
import functools
import inspect
import json
import logging
import time
from collections.abc import Awaitable, Callable, Sequence
from dataclasses import dataclass
from typing import Any, cast

from aiohttp import web
from dex import (
    AesGcmPayloadCodec,
    PayloadContext,
    PayloadEncryptionError,
    PayloadErrorCategory,
    is_valid_payload_key_id,
)
from dex.dexpb import dex_pb2 as pb
from google.protobuf import json_format
from google.protobuf.message import DecodeError

from dex_codec_server.auth import (
    AnyPayloadCodec,
    CallerForbiddenError,
    CallerIdentity,
    CallerPayloadCodecSelector,
    CallerUnauthenticatedError,
    RequestAuthenticator,
)
from dex_codec_server.config import PayloadSizeLimits
from dex_codec_server.cors import CorsOriginAllowList
from dex_codec_server.json_logging import LOG_FIELDS_ATTRIBUTE
from dex_codec_server.value_json import measure_value_json_bytes, serialize_value_json

AES_GCM_NONCE_AND_TAG_BYTES = 28
RESERVED_ENCODING_PREFIX = "dex/"
# Fixed by the format spec, section 7.
MAX_METADATA_ENTRIES = 16
MAX_METADATA_TOTAL_BYTES = 4096
_ENCRYPTABLE_KINDS = frozenset(
    {"string_value", "obj_value", "int_value", "double_value", "bool_value"}
)

# Fixed client messages; codec and key provider error text is never sent or logged.
DECODE_CATEGORY_MESSAGES = {
    PayloadErrorCategory.MALFORMED: "envelope is malformed",
    PayloadErrorCategory.UNSUPPORTED_MARKER: "envelope format version is not supported",
    PayloadErrorCategory.UNSUPPORTED_CIPHER: "envelope cipher is not supported",
    PayloadErrorCategory.UNSUPPORTED_COMPRESSION: "envelope compression is not supported",
    PayloadErrorCategory.UNKNOWN_KEY: "envelope key is not available to this server",
    PayloadErrorCategory.AUTHENTICATION_FAILED: "envelope failed authentication",
    PayloadErrorCategory.LIMIT_EXCEEDED: "envelope exceeds a size limit",
    PayloadErrorCategory.INVALID_PLAINTEXT: "decrypted plaintext is not an encryptable Value",
}

_LOG_FIELDS_KEY = web.RequestKey("log_fields", dict)


class CodecRequestError(Exception):
    """A protocol error. ``message`` never holds payload bytes, plaintext, or keys."""

    def __init__(
        self,
        status: int,
        category: str,
        message: str,
        key_id: str = "",
        index: int | None = None,
    ) -> None:
        """Create the error.

        Args:
            status: The HTTP status.
            category: The spec error category.
            message: A fixed client message.
            key_id: A validated key ID for the access log, or ``""``.
            index: The first failing entry for 422 errors.
        """
        super().__init__(category)
        self.status = status
        self.category = category
        self.message = message
        self.key_id = key_id
        self.index = index

    def at(self, index: int) -> CodecRequestError:
        """Name the failing entry and return this error."""
        self.index = index
        return self


def _parse_json_integer(text: str) -> int | float:
    # The protocol writes negative zero as -0; int("-0") would drop its sign.
    if text == "-0":
        return -0.0
    return int(text)


def _malformed_request(message: str) -> CodecRequestError:
    return CodecRequestError(400, "malformed", message)


def _internal_error() -> CodecRequestError:
    return CodecRequestError(500, "internal", "internal error")


def _entry_error(category: str, message: str, key_id: str = "") -> CodecRequestError:
    return CodecRequestError(422, category, message, key_id)


def _limit_exceeded(message: str) -> CodecRequestError:
    return CodecRequestError(413, "limit_exceeded", message)


@dataclass(frozen=True)
class _AdmittedRequest:
    codec: AnyPayloadCodec
    context: PayloadContext
    items: list[Any]


class PayloadCodecHttpServer:
    """Serve ``/encode``, ``/decode``, and ``/healthz``."""

    def __init__(
        self,
        *,
        authenticator: RequestAuthenticator,
        codec_selector: CallerPayloadCodecSelector,
        cors_origins: CorsOriginAllowList,
        payload_size_limits: PayloadSizeLimits,
        max_request_body_bytes: int,
        max_items_per_request: int,
        max_decoded_bytes_per_request: int,
        logger: logging.Logger,
    ) -> None:
        """Create the server from its collaborators and limits.

        Args:
            authenticator: Authenticates each request before its body is read.
            codec_selector: Picks the codec from the authenticated caller.
            cors_origins: The allowed Dex Web origins.
            payload_size_limits: The spec section 7 limits.
            max_request_body_bytes: Larger bodies get 413.
            max_items_per_request: More entries get 413.
            max_decoded_bytes_per_request: Bounds the Value JSON of one ``/decode`` response.
            logger: Receives one access line per request and fixed internal error codes.
        """
        self._authenticator = authenticator
        self._codec_selector = codec_selector
        self._cors_origins = cors_origins
        self._limits = payload_size_limits
        self._max_request_body_bytes = max_request_body_bytes
        self._max_items_per_request = max_items_per_request
        self._max_decoded_bytes_per_request = max_decoded_bytes_per_request
        self._logger = logger

    def create_application(self) -> web.Application:
        """Return the aiohttp application with every route."""
        application = web.Application(client_max_size=self._max_request_body_bytes)
        application.router.add_route("*", "/encode", self._wrap_endpoint(self.serve_encode))
        application.router.add_route("*", "/decode", self._wrap_endpoint(self.serve_decode))
        application.router.add_get("/healthz", self.serve_health_check, allow_head=False)
        return application

    async def serve_health_check(self, request: web.Request) -> web.Response:
        """Answer load balancer health checks without authentication."""
        return _json_response(200, '{"status":"ok"}')

    async def serve_decode(self, request: web.Request) -> web.Response:
        """Decode envelopes into Value JSON, one at a time, within the decoded-bytes budget."""
        admitted = await self._admit_request(request, "envelopes")
        if not all(isinstance(item, str) for item in admitted.items):
            raise _malformed_request("envelopes must be strings")
        value_texts: list[str] = []
        decoded_bytes = 0
        for index, envelope_text in enumerate(admitted.items):
            try:
                value = await self._decode_envelope(
                    admitted.codec, admitted.context, envelope_text
                )
            except CodecRequestError as error:
                raise error.at(index) if error.status == 422 else error
            # Measured before serializing: escaping can make the JSON six times the plaintext.
            decoded_bytes += measure_value_json_bytes(value)
            if decoded_bytes > self._max_decoded_bytes_per_request:
                raise _limit_exceeded(
                    "decoded values exceed the server's per-request limit; "
                    "send fewer envelopes per request"
                )
            value_texts.append(serialize_value_json(value))
        return _json_response(200, '{"values":[' + ",".join(value_texts) + "]}")

    async def serve_encode(self, request: web.Request) -> web.Response:
        """Encode Value JSON into envelopes."""
        admitted = await self._admit_request(request, "values")
        plaintexts = [
            self._serialize_encodable_value(value_json, index)
            for index, value_json in enumerate(admitted.items)
        ]
        envelope_texts: list[str] = []
        if plaintexts:
            envelopes = await self._call_codec_encode(admitted.codec, admitted.context, plaintexts)
            for index, envelope in enumerate(envelopes):
                envelope_texts.append(self._serialize_envelope(envelope, index))
        return _json_response(200, json.dumps({"envelopes": envelope_texts}))

    async def _admit_request(self, request: web.Request, item_field: str) -> _AdmittedRequest:
        """Check method, encoding, authentication, authorization, body size, and item count."""
        if request.method != "POST":
            raise _malformed_request("use POST")
        if request.headers.get("Content-Encoding", "identity").lower() != "identity":
            raise _malformed_request("compressed request bodies are not accepted")
        # Requiring JSON also forces a CORS preflight for every cross-origin request.
        if request.content_type != "application/json":
            raise _malformed_request("Content-Type must be application/json")
        try:
            caller = await self._authenticator.authenticate_request(request)
        except CallerUnauthenticatedError:
            raise CodecRequestError(
                401, "unauthenticated", "credentials are missing or invalid"
            ) from None
        log_fields = request[_LOG_FIELDS_KEY]
        log_fields["caller"] = caller.subject
        codec = await self._select_codec(caller)
        content = await self._read_body(request)
        try:
            body = json.loads(content, parse_int=_parse_json_integer)
        except (ValueError, RecursionError):
            raise _malformed_request("request body is not valid protocol JSON") from None
        if not isinstance(body, dict):
            raise _malformed_request("request body is not valid protocol JSON")
        context = body.get("context")
        # flow_type may be empty: clients that know only a Flow ID send "".
        if (
            not isinstance(context, dict)
            or not isinstance(context.get("flow_type", ""), str)
            or not isinstance(context.get("flow_id"), str)
            or not context["flow_id"]
        ):
            raise _malformed_request("context.flow_id is required; context.flow_type is a string")
        items = body.get(item_field)
        if not isinstance(items, list):
            raise _malformed_request(f"the request has no {item_field} list")
        log_fields["item_count"] = len(items)
        if len(items) > self._max_items_per_request:
            raise _limit_exceeded("the request has more entries than the server's limit")
        return _AdmittedRequest(
            codec=codec,
            context=PayloadContext(
                flow_type=context.get("flow_type", ""), flow_id=context["flow_id"]
            ),
            items=items,
        )

    async def _select_codec(self, caller: CallerIdentity) -> AnyPayloadCodec:
        try:
            return await self._codec_selector.select_payload_codec(caller)
        except CallerForbiddenError:
            raise CodecRequestError(
                403, "forbidden", "the caller may not use payload keys"
            ) from None
        except Exception:
            self._log_internal_error("codec_selector_failed")
            raise _internal_error() from None

    async def _read_body(self, request: web.Request) -> bytes:
        too_large = _limit_exceeded("request body exceeds the server's limit")
        if (request.content_length or 0) > self._max_request_body_bytes:
            raise too_large
        try:
            return await request.read()
        except web.HTTPRequestEntityTooLarge:
            raise too_large from None
        except Exception:
            raise _malformed_request("request body could not be read") from None

    async def _decode_envelope(
        self, codec: AnyPayloadCodec, context: PayloadContext, envelope_text: str
    ) -> pb.Value:
        """Run decode steps 3 to 14 of the format spec for one envelope."""
        envelope = self._parse_envelope_text(envelope_text)
        key_id = envelope.key_id
        await self._check_built_in_data_size(codec, context, envelope)
        plaintext = await self._call_codec_decode(codec, context, envelope)
        if len(plaintext) > self._limits.max_plaintext_bytes:
            raise _entry_error("limit_exceeded", "plaintext exceeds the server's limit", key_id)
        value = pb.Value()
        try:
            value.ParseFromString(plaintext)
        except DecodeError:
            raise _entry_error("invalid_plaintext", "plaintext is not a Value", key_id) from None
        problem = describe_unencryptable_value(value)
        if problem:
            raise _entry_error("invalid_plaintext", problem, key_id)
        return value

    def _parse_envelope_text(self, envelope_text: str) -> pb.PayloadEnvelope:
        """Run steps 3 to 7 before any codec call: sizes, base64, parse, header."""
        limits = self._limits
        if len(envelope_text) > limits.max_encoded_text_bytes:
            raise _entry_error("limit_exceeded", "envelope text exceeds the server's limit")
        not_canonical = _entry_error(
            "malformed", "envelope is not canonical padded standard base64"
        )
        if len(envelope_text) % 4 != 0:
            raise not_canonical
        decoded_length = len(envelope_text) // 4 * 3 - (
            len(envelope_text) - len(envelope_text.rstrip("="))
        )
        if decoded_length > limits.max_envelope_bytes:
            raise _entry_error("limit_exceeded", "envelope exceeds the server's limit")
        try:
            envelope_bytes = base64.b64decode(envelope_text, validate=True)
        except (binascii.Error, ValueError):
            raise not_canonical from None
        if base64.b64encode(envelope_bytes).decode("ascii") != envelope_text:
            raise not_canonical
        envelope = pb.PayloadEnvelope()
        try:
            envelope.ParseFromString(envelope_bytes)
        except DecodeError:
            raise _entry_error("malformed", "envelope is not a PayloadEnvelope") from None
        if not is_valid_payload_key_id(envelope.key_id):
            raise _entry_error("malformed", "envelope key id is invalid")
        key_id = envelope.key_id
        metadata_bytes = sum(
            len(name.encode()) + len(value) for name, value in envelope.metadata.items()
        )
        if (
            len(envelope.metadata) > MAX_METADATA_ENTRIES
            or metadata_bytes > MAX_METADATA_TOTAL_BYTES
        ):
            raise _entry_error("limit_exceeded", "envelope metadata exceeds the limits", key_id)
        if envelope.cipher == pb.PAYLOAD_CIPHER_UNSPECIFIED:
            raise _entry_error("unsupported_cipher", "envelope cipher is unspecified", key_id)
        return envelope

    async def _check_built_in_data_size(
        self, codec: AnyPayloadCodec, context: PayloadContext, envelope: pb.PayloadEnvelope
    ) -> None:
        """Run step 11 for the built-in codec in spec order, before any AEAD output exists.

        The public codec has no data limit. For oversized data, the codec first
        decodes a header-only probe: it reports steps 7 to 10, and its expected
        ``authentication_failed`` means those steps passed.
        """
        if (
            not isinstance(codec, AesGcmPayloadCodec)
            or len(envelope.data) <= self._limits.max_data_bytes
        ):
            return
        probe = pb.PayloadEnvelope(
            cipher=envelope.cipher,
            key_id=envelope.key_id,
            compression=envelope.compression,
            metadata=dict(envelope.metadata),
            data=bytes(AES_GCM_NONCE_AND_TAG_BYTES),
        )
        try:
            await _call_payload_codec(
                codec.decode, context, [probe], self._decode_keyword_arguments(codec)
            )
        except PayloadEncryptionError as error:
            if error.category is not PayloadErrorCategory.AUTHENTICATION_FAILED:
                raise self._classify_decode_error(error.category, envelope.key_id) from None
        except Exception:
            self._log_internal_error("codec_decode_failed")
            raise _internal_error() from None
        raise _entry_error(
            "limit_exceeded", "envelope data exceeds the server's limit", envelope.key_id
        )

    async def _call_codec_decode(
        self, codec: AnyPayloadCodec, context: PayloadContext, envelope: pb.PayloadEnvelope
    ) -> bytes:
        try:
            plaintexts = await _call_payload_codec(
                codec.decode, context, [envelope], self._decode_keyword_arguments(codec)
            )
        except PayloadEncryptionError as error:
            # The request envelope's validated key ID, never the error's: a custom codec could fill it.
            raise self._classify_decode_error(error.category, envelope.key_id) from None
        except Exception:
            self._log_internal_error("codec_decode_failed")
            raise _internal_error() from None
        if len(plaintexts) != 1 or not isinstance(plaintexts[0], (bytes, bytearray)):
            self._log_internal_error("decode_result_mismatch")
            raise _internal_error()
        return bytes(plaintexts[0])

    def _decode_keyword_arguments(self, codec: AnyPayloadCodec) -> dict[str, int]:
        """Pass the plaintext limit to a ``decode`` that declares it, as the SDK does."""
        if _declares_plaintext_limit(codec.decode):
            return {"max_plaintext_bytes": self._limits.max_plaintext_bytes}
        return {}

    def _classify_decode_error(self, category: object, key_id: str) -> CodecRequestError:
        # codec_failed and unknown categories describe no envelope, so they answer 500.
        message = DECODE_CATEGORY_MESSAGES.get(cast(PayloadErrorCategory, category))
        if message is None:
            self._log_internal_error("codec_decode_failed")
            return _internal_error()
        return _entry_error(
            cast(PayloadErrorCategory, category).value, f"{message} (key id {key_id!r})", key_id
        )

    def _serialize_encodable_value(self, value_json: object, index: int) -> bytes:
        value = pb.Value()
        if not isinstance(value_json, dict):
            raise _entry_error("malformed", "entry is not dex.Value JSON").at(index)
        try:
            json_format.ParseDict(value_json, value)
        except Exception:
            raise _entry_error("malformed", "entry is not dex.Value JSON").at(index) from None
        problem = describe_unencryptable_value(value)
        if problem:
            raise _entry_error("invalid_plaintext", problem).at(index)
        plaintext = value.SerializeToString()
        if (
            len(plaintext) > self._limits.max_plaintext_bytes
            or len(plaintext) + AES_GCM_NONCE_AND_TAG_BYTES > self._limits.max_data_bytes
        ):
            raise _entry_error("limit_exceeded", "value exceeds the server's limit").at(index)
        return plaintext

    async def _call_codec_encode(
        self, codec: AnyPayloadCodec, context: PayloadContext, plaintexts: list[bytes]
    ) -> Sequence[pb.PayloadEnvelope]:
        try:
            envelopes = await _call_payload_codec(codec.encode, context, plaintexts)
        except Exception:
            self._log_internal_error("codec_encode_failed")
            raise _internal_error() from None
        if len(envelopes) != len(plaintexts) or not all(
            isinstance(envelope, pb.PayloadEnvelope) for envelope in envelopes
        ):
            self._log_internal_error("encode_result_mismatch")
            raise _internal_error()
        return envelopes

    def _serialize_envelope(self, envelope: pb.PayloadEnvelope, index: int) -> str:
        envelope_bytes = envelope.SerializeToString()
        if len(envelope_bytes) > self._limits.max_envelope_bytes:
            key_id = envelope.key_id if is_valid_payload_key_id(envelope.key_id) else ""
            raise _entry_error(
                "limit_exceeded", "envelope exceeds the server's limit", key_id
            ).at(index)
        return base64.b64encode(envelope_bytes).decode("ascii")

    def _wrap_endpoint(
        self, handler: Callable[[web.Request], Awaitable[web.Response]]
    ) -> Callable[[web.Request], Awaitable[web.StreamResponse]]:
        """Add the access log, CORS, and error bodies around an endpoint."""

        async def serve_endpoint(request: web.Request) -> web.StreamResponse:
            started = time.monotonic()
            log_fields: dict[str, Any] = {"caller": "", "item_count": 0}
            request[_LOG_FIELDS_KEY] = log_fields
            response = await self._answer_with_cors(request, handler)
            self._write_access_log(request, response.status, started, log_fields)
            return response

        return serve_endpoint

    async def _answer_with_cors(
        self, request: web.Request, handler: Callable[[web.Request], Awaitable[web.Response]]
    ) -> web.StreamResponse:
        """Run CORS before authentication: preflights carry no credentials.

        Requests without ``Origin`` come from non-browser clients and pass.
        """
        origin = request.headers.get("Origin", "")
        if origin and not self._cors_origins.is_allowed_origin(origin):
            response = self._error_response(
                request, CodecRequestError(403, "forbidden", "origin is not allowed")
            )
            response.headers.add("Vary", "Origin")
            return response
        if origin and request.method == "OPTIONS" and "Access-Control-Request-Method" in request.headers:
            response = web.Response(status=204)
            CorsOriginAllowList.set_preflight_headers(response)
        else:
            try:
                response = await handler(request)
            except CodecRequestError as error:
                response = self._error_response(request, error)
            except Exception:
                self._log_internal_error("unexpected_handler_failure")
                response = self._error_response(request, _internal_error())
        if origin:
            response.headers.add("Vary", "Origin")
            self._cors_origins.set_allowed_origin_headers(response, origin)
        return response

    def _error_response(self, request: web.Request, error: CodecRequestError) -> web.Response:
        log_fields = request[_LOG_FIELDS_KEY]
        log_fields["error_category"] = error.category
        log_fields["key_id"] = error.key_id
        if error.index is not None:
            log_fields["error_index"] = error.index
        detail: dict[str, Any] = {"category": error.category, "message": error.message}
        if error.index is not None:
            detail["index"] = error.index
        return _json_response(error.status, json.dumps({"error": detail}))

    def _write_access_log(
        self, request: web.Request, status: int, started: float, log_fields: dict[str, Any]
    ) -> None:
        fields = {
            "method": request.method,
            "path": request.path,
            "status": status,
            "duration_ms": round((time.monotonic() - started) * 1000),
            "origin": request.headers.get("Origin", ""),
            **log_fields,
        }
        self._logger.info("codec request", extra={LOG_FIELDS_ATTRIBUTE: fields})

    def _log_internal_error(self, internal_error_code: str) -> None:
        self._logger.error(
            "codec request failed",
            extra={LOG_FIELDS_ATTRIBUTE: {"internal_error_code": internal_error_code}},
        )


def describe_unencryptable_value(value: pb.Value) -> str:
    """Return why ``value`` is never encrypted, or ``""`` for the encryptable kinds."""
    kind = value.WhichOneof("kind")
    if kind is None:
        return "value has no kind"
    if kind not in _ENCRYPTABLE_KINDS:
        return "null values and blob references are never encrypted"
    if kind == "obj_value" and value.obj_value.encoding.startswith(RESERVED_ENCODING_PREFIX):
        return "object encodings starting with dex/ are reserved"
    return ""


async def _call_payload_codec(
    method: Callable[..., Any],
    context: PayloadContext,
    items: list[Any],
    keyword_arguments: dict[str, int] | None = None,
) -> Sequence[Any]:
    """Await an ``AsyncPayloadCodec`` method; run a sync codec off the event loop."""
    arguments = keyword_arguments or {}
    if inspect.iscoroutinefunction(method):
        return cast(Sequence[Any], await method(context, items, **arguments))
    return cast(
        Sequence[Any], await asyncio.to_thread(functools.partial(method, **arguments), context, items)
    )


def _declares_plaintext_limit(decode: Callable[..., Any]) -> bool:
    """Return whether ``decode`` accepts the optional ``max_plaintext_bytes`` keyword."""
    try:
        parameters = inspect.signature(decode).parameters.values()
    except (TypeError, ValueError):
        return False
    return any(
        parameter.kind is inspect.Parameter.VAR_KEYWORD
        or (
            parameter.name == "max_plaintext_bytes"
            and parameter.kind
            in (inspect.Parameter.KEYWORD_ONLY, inspect.Parameter.POSITIONAL_OR_KEYWORD)
        )
        for parameter in parameters
    )


def _json_response(status: int, body: str) -> web.Response:
    return web.Response(
        status=status,
        text=body,
        content_type="application/json",
        headers={"Cache-Control": "no-store"},
    )
