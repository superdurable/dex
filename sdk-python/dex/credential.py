# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Bearer credentials for Dex service calls."""

from __future__ import annotations

import copy
import http.client
import ipaddress
import json
import math
import os
import re
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Awaitable, Protocol


_JWT_PATTERN = re.compile(
    r"(?<![A-Za-z0-9_-])[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}(?![A-Za-z0-9_-])"
)


class Credential(Protocol):
    """Supply a bearer token for each Client or Worker service call.

    token may be called concurrently. AsyncClient and AsyncWorker accept an
    awaitable token; synchronous callers require a string. Synchronous tokens
    run on the calling thread for sync APIs and in an SDK-owned, four-thread
    executor for async APIs. Each Client or Worker creates its executor lazily
    and closes it without waiting for a blocked token call.
    A blocked token keeps one executor thread busy until it returns. An unclosed
    Client or Worker may retain up to four idle credential threads.
    Declare token with async def to invoke it on the event loop. A synchronous
    token method may return a coroutine, which is awaited on the event loop.
    A loop-bound Future from another loop raises TypeError.
    Async calls bound token lookup by the RPC deadline when one is set. Pull
    stream opens use a 30-second lookup budget. Sync calls do not bound token
    lookup separately, so synchronous implementations must return promptly.
    Client calls raise a token exception unchanged, before contacting Dex.
    Worker startup may wrap it with startup context. The token travels
    over Dex's plaintext gRPC connection; protect the network path.

    Example:
        >>> class FixedCredential:
        ...     def token(self) -> str:
        ...         return "my-token"
        >>> credential = FixedCredential()
        >>> options = ClientOptions(credential=credential)
    """

    def token(self) -> str | Awaitable[str]:
        """Return the current bearer token, or raise when unavailable.

        Returns:
            Token without the Bearer prefix, or an awaitable for async callers.

        Raises:
            Exception: Any failure prevents the service call from reaching Dex.
        """
        ...


class FileCredential:
    """Read a bearer token from a file and pick up rotations automatically.

    The file is read during construction. Its mtime and size are checked at most
    once per second, and changed contents are reloaded before the next call.
    Surrounding whitespace is removed. A file that becomes empty or unreadable
    fails subsequent calls until restored. Concurrent calls share a lock.
    Async callers check the file in an executor, outside the event loop.

    Example:
        >>> credential = FileCredential("/var/run/secrets/dex/token")
        >>> client = Client(registry, cache, ClientOptions(credential=credential))

    Args:
        path: Path to the token file.

    Raises:
        OSError: The file cannot be read during construction.
        ValueError: The file has no token after trimming whitespace.
        UnicodeError: The file does not contain valid UTF-8 text.
    """

    def __init__(self, path: str | os.PathLike[str]) -> None:
        """Read the initial token from path and fail if it is unavailable or empty.

        Args:
            path: File containing the bearer token.

        Raises:
            OSError: The file cannot be read.
            ValueError: The file has no token after trimming whitespace.
            UnicodeError: The file does not contain valid UTF-8 text.
        """
        self._path = Path(path)
        self._lock = threading.Lock()
        self._clock = time.monotonic
        self._token = ""
        self._mtime_ns = -1
        self._size = -1
        self._checked_at = 0.0
        self._error: OSError | UnicodeError | ValueError | None = None
        with self._lock:
            self._refresh()
            if self._error is not None:
                raise copy.copy(self._error)

    def token(self) -> str:
        """Return the current trimmed token, checking for file changes once per second.

        Returns:
            The token without the Bearer prefix.

        Raises:
            OSError: The file is no longer readable.
            ValueError: The file is empty after trimming whitespace.
            UnicodeError: The file does not contain valid UTF-8 text.
        """
        with self._lock:
            if self._clock() - self._checked_at >= 1.0:
                self._refresh()
            if self._error is not None:
                raise copy.copy(self._error)
            return self._token

    def _refresh(self) -> None:
        self._checked_at = self._clock()
        try:
            stat = self._path.stat()
            if (
                self._error is None
                and self._token
                and stat.st_mtime_ns == self._mtime_ns
                and stat.st_size == self._size
            ):
                return
            token = self._path.read_text(encoding="utf-8").strip()
            if not token:
                raise ValueError(f"credential file {self._path} is empty")
        except (OSError, UnicodeError, ValueError) as error:
            self._token = ""
            self._error = copy.copy(error)
            return
        self._token = token
        self._mtime_ns = stat.st_mtime_ns
        self._size = stat.st_size
        self._error = None


class ApiKeyExchangeError(Exception):
    """Report a failed API key exchange without exposing credential secrets.

    The message identifies the token URL and HTTP status when available. It
    includes the endpoint's error and error_description fields, each limited to
    200 characters. It never includes the response body, API key, issued access
    tokens, or JWT-shaped strings.
    Client calls raise this error unchanged. Pull Workers log it and retry.
    """


class _NoTokenRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(
        self,
        req: urllib.request.Request,
        fp: object,
        code: int,
        msg: str,
        headers: object,
        newurl: str,
    ) -> None:
        return None


class _TokenExchange:
    def __init__(self, deadline: float) -> None:
        self.deadline = deadline
        self.event = threading.Event()
        self.error: ApiKeyExchangeError | None = None
        self.timer: threading.Timer | None = None


class ApiKeyCredential:
    """Exchange an API key for short-lived bearer tokens before Dex calls.

    Use this when an application holds a long-lived API key and a token endpoint
    issues access tokens for a Dex server ID. Use FileCredential when another
    process writes and rotates a bearer token file instead. The first token()
    call exchanges synchronously. Later calls start one background refresh after
    80% of the token lifetime and keep serving the valid token. Concurrent
    callers share one exchange. Failed refreshes retry no more often than every
    five seconds while the old token remains valid. An expired token makes
    callers wait up to exchange_timeout for the in-flight exchange. Dex unary
    calls retry once with a fresh token after UNAUTHENTICATED. Pull Worker
    streams reconnect once immediately with a fresh token after
    UNAUTHENTICATED; a second rejection uses the normal backoff.

    The token URL must use HTTPS. HTTP is allowed only on localhost, 127.0.0.0/8,
    or ::1. Loopback HTTP bypasses environment proxies; HTTPS honors them.
    Redirects are refused. The instance is thread-safe. close() is optional
    and can be called after all Clients and Workers sharing the credential are
    closed. It waits only through the current exchange deadline. Formatting
    this credential or an exchange error does not reveal secrets.

    Example:
        >>> credential = ApiKeyCredential(
        ...     "https://auth.example.com/token", "dex-server", "application-api-key"
        ... )
        >>> client = Client(registry, cache, ClientOptions(credential=credential))
        >>> try:
        ...     client.health_check()
        ... finally:
        ...     client.close()
        ...     credential.close()

    Args:
        token_url: Absolute token endpoint URL.
        server_id: Server ID used to scope the issued access token.
        api_key: API key exchanged for access tokens.
        exchange_timeout: Maximum seconds for each exchange, including connect
            and response; defaults to 10 seconds.

    Raises:
        ValueError: An input is empty, the URL is invalid or insecure, or the
            timeout is not positive.
    """

    def __init__(
        self,
        token_url: str,
        server_id: str,
        api_key: str,
        *,
        exchange_timeout: float = 10.0,
    ) -> None:
        """Validate exchange settings without contacting the token endpoint.

        Args:
            token_url: Absolute HTTPS URL, or loopback HTTP URL.
            server_id: Server ID to send in the exchange request.
            api_key: API key to exchange for an access token.
            exchange_timeout: Positive maximum exchange time in seconds.

        Raises:
            ValueError: A required string is empty, the URL is invalid or
                insecure, or the timeout is not positive and finite.
        """
        if any(
            not isinstance(value, str) or not value.strip()
            for value in (token_url, server_id, api_key)
        ):
            raise ValueError("token_url, server_id, and api_key must be non-empty")
        parsed = urllib.parse.urlsplit(token_url)
        try:
            host = parsed.hostname
            port = parsed.port
        except ValueError:
            raise ValueError("token_url must be an absolute HTTPS URL") from None
        if not host or port == 0 or parsed.scheme not in ("https", "http"):
            raise ValueError("token_url must be an absolute HTTPS URL")
        if (
            parsed.username is not None
            or parsed.password is not None
            or parsed.fragment
        ):
            raise ValueError("token_url must not contain user info or a fragment")
        if parsed.scheme == "http" and not self._is_loopback(host):
            raise ValueError("token_url must use HTTPS outside loopback")
        if (
            type(exchange_timeout) not in (float, int)
            or not math.isfinite(exchange_timeout)
            or exchange_timeout <= 0
        ):
            raise ValueError("exchange_timeout must be positive")
        self._token_url = token_url
        self._server_id = server_id
        self._api_key = api_key
        self._exchange_timeout = float(exchange_timeout)
        if parsed.scheme == "http":
            self._http_client = urllib.request.build_opener(
                urllib.request.ProxyHandler({}), _NoTokenRedirect
            )
        else:
            self._http_client = urllib.request.build_opener(_NoTokenRedirect)
        self._clock = time.monotonic
        self._lock = threading.Lock()
        self._token = ""
        self._remembered_tokens: frozenset[str] = frozenset()
        self._refresh_at = 0.0
        self._expires_at = 0.0
        self._retry_at = 0.0
        self._in_flight: _TokenExchange | None = None
        self._exchange_thread: threading.Thread | None = None
        self._closed = False

    def token(self) -> str:
        """Return an access token, exchanging or refreshing it when necessary.

        A valid token returns immediately, including during background refresh.
        First and expired-token calls wait up to exchange_timeout. Concurrent
        calls share the same exchange. Exchange failures raise
        ApiKeyExchangeError. Calls after close() raise RuntimeError.

        Returns:
            Access token without the Bearer prefix.

        Raises:
            ApiKeyExchangeError: The endpoint, response, or transport failed.
            RuntimeError: The credential is closed.
        """
        return self._token_with_timeout(None)

    def _token_with_timeout(self, timeout: float | None) -> str:
        wait_deadline = time.monotonic() + timeout if timeout is not None else math.inf
        with self._lock:
            if self._closed:
                raise RuntimeError("API key credential is closed")
            now = self._clock()
            if self._token and now < self._expires_at:
                if now >= self._refresh_at and now >= self._retry_at:
                    self._start_exchange()
                return self._token
            if self._in_flight is None:
                exchange = self._start_exchange()
            else:
                exchange = self._in_flight
        if not exchange.event.wait(
            max(0.0, min(exchange.deadline, wait_deadline) - time.monotonic())
        ):
            if time.monotonic() >= exchange.deadline:
                self._expire_exchange(exchange)
            else:
                raise TimeoutError("credential lookup exceeded RPC deadline")
        with self._lock:
            if self._closed:
                raise RuntimeError("API key credential is closed")
            if exchange.error is not None:
                raise copy.copy(exchange.error)
            if self._token and self._clock() < self._expires_at:
                return self._token
        raise self._error(None, "exchange produced no valid token")

    def close(self) -> None:
        """Stop using this credential and wait up to its current exchange deadline.

        Closing is optional. Close a shared credential after all Clients and
        Workers using it have closed. Subsequent token() calls raise
        RuntimeError. A network operation may continue in a daemon thread after
        the deadline when the operating system cannot interrupt it.
        """
        with self._lock:
            self._closed = True
            exchange_thread = self._exchange_thread
            exchange = self._in_flight
        if (
            exchange_thread is not None
            and exchange_thread is not threading.current_thread()
        ):
            exchange_thread.join(
                max(0.0, exchange.deadline - time.monotonic())
                if exchange is not None
                else 0.0
            )

    def _reject_token(self, rejected_token: str) -> None:
        with self._lock:
            if self._token == rejected_token:
                self._token = ""
                self._refresh_at = 0.0
                self._expires_at = 0.0
                self._retry_at = 0.0

    @staticmethod
    def _is_loopback(host: str) -> bool:
        if host.lower() == "localhost":
            return True
        try:
            return ipaddress.ip_address(host).is_loopback
        except ValueError:
            return False

    def _start_exchange(self) -> _TokenExchange:
        if self._in_flight is not None:
            return self._in_flight
        exchange = _TokenExchange(time.monotonic() + self._exchange_timeout)
        exchange_thread = threading.Thread(
            target=self._run_exchange,
            args=(exchange,),
            name="dex-python-api-key-exchange",
            daemon=True,
        )
        timer = threading.Timer(
            self._exchange_timeout, self._expire_exchange, args=(exchange,)
        )
        timer.daemon = True
        exchange.timer = timer
        try:
            timer.start()
            exchange_thread.start()
        except Exception as error:
            timer.cancel()
            exchange.error = self._error(None, type(error).__name__)
            exchange.event.set()
            self._retry_at = self._clock() + 5.0
            return exchange
        self._in_flight = exchange
        self._exchange_thread = exchange_thread
        return exchange

    def _run_exchange(self, exchange: _TokenExchange) -> None:
        started_at = self._clock()
        token = ""
        lifetime = 0
        failure: ApiKeyExchangeError | None = None
        try:
            token, lifetime = self._exchange(exchange.deadline)
            if time.monotonic() >= exchange.deadline:
                failure = self._error(None, "exchange timed out")
        except ApiKeyExchangeError as error:
            failure = error
        except Exception as error:
            failure = self._error(None, type(error).__name__)
        finally:
            if exchange.timer is not None:
                exchange.timer.cancel()
            with self._lock:
                if self._in_flight is exchange:
                    if not self._closed and failure is None:
                        self._token = token
                        self._remembered_tokens = self._remembered_tokens | {token}
                        try:
                            self._refresh_at = started_at + lifetime * 0.8
                            self._expires_at = started_at + lifetime
                        except OverflowError:
                            self._refresh_at = math.inf
                            self._expires_at = math.inf
                        self._retry_at = 0.0
                    elif failure is not None:
                        self._retry_at = self._clock() + 5.0
                    exchange.error = failure
                    self._in_flight = None
                    exchange.event.set()

    def _expire_exchange(self, exchange: _TokenExchange) -> None:
        with self._lock:
            if self._in_flight is exchange:
                exchange.error = self._error(None, "exchange timed out")
                self._retry_at = self._clock() + 5.0
                self._in_flight = None
                exchange.event.set()

    def _exchange(self, deadline: float) -> tuple[str, int]:
        request_body = json.dumps(
            {"server_id": self._server_id, "api_key": self._api_key},
            separators=(",", ":"),
        ).encode("utf-8")
        request = urllib.request.Request(
            self._token_url,
            data=request_body,
            headers={"Content-Type": "application/json", "Accept": "application/json"},
            method="POST",
        )
        try:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise self._error(None, "exchange timed out")
            with self._http_client.open(request, timeout=remaining) as response:
                status = response.status
                body = self._read_response(response, deadline, status)
        except urllib.error.HTTPError as error:
            status = error.code
            if 300 <= status < 400:
                raise self._error(status, "redirect refused") from None
            try:
                body = self._read_response(error, deadline, status)
            except (OSError, http.client.HTTPException) as failure:
                raise self._error(status, type(failure).__name__) from None
        except (OSError, ValueError, http.client.HTTPException) as error:
            reason = type(error).__name__
            if isinstance(error, urllib.error.URLError):
                reason += f"({type(error.reason).__name__})"
            raise self._error(None, reason) from None
        try:
            payload = json.loads(body)
        except (ValueError, UnicodeError):
            raise self._error(status, "invalid JSON response") from None
        if not isinstance(payload, dict):
            raise self._error(status, "response must be a JSON object")
        if status != 200:
            raise self._error(status, "exchange rejected", payload)
        token = payload.get("access_token")
        token_type = payload.get("token_type")
        lifetime = payload.get("expires_in")
        if not isinstance(token, str) or not token:
            raise self._error(status, "invalid access_token", payload)
        if not isinstance(token_type, str) or token_type.lower() != "bearer":
            raise self._error(status, "invalid token_type", payload)
        if type(lifetime) is not int or lifetime <= 0:
            raise self._error(status, "invalid expires_in", payload)
        return token, lifetime

    def _read_response(self, response: object, deadline: float, status: int) -> bytes:
        body = bytearray()
        while True:
            if time.monotonic() >= deadline:
                raise self._error(status, "exchange timed out")
            chunk = response.read(1)  # type: ignore[attr-defined]
            if not chunk:
                return bytes(body)
            body.extend(chunk)
            if len(body) > 1024 * 1024:
                raise self._error(status, "response exceeds 1 MiB")

    def _error(
        self, status: int | None, reason: str, payload: dict[str, object] | None = None
    ) -> ApiKeyExchangeError:
        status_text = str(status) if status is not None else "unavailable"
        message = f"token exchange at {self._token_url} failed (HTTP status {status_text}): {reason}"
        secrets = {self._api_key, self._token, *self._remembered_tokens}
        if payload is not None:
            response_token = payload.get("access_token")
            if isinstance(response_token, str):
                secrets.add(response_token)
            for name in ("error", "error_description"):
                detail = payload.get(name)
                if isinstance(detail, str):
                    message += f"; {name}={self._redact(detail, secrets)[:200]}"
        message = self._redact(message, secrets)
        return ApiKeyExchangeError(message)

    @staticmethod
    def _redact(value: str, secrets: set[str]) -> str:
        for secret in sorted(secrets, key=len, reverse=True):
            if secret:
                value = value.replace(secret, "[REDACTED]")
        return _JWT_PATTERN.sub("[REDACTED]", value)
