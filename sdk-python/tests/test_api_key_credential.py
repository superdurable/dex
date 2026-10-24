# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import asyncio
import http.client
import json
import queue
import socketserver
import threading
import time
from collections.abc import Iterator
from concurrent.futures import ThreadPoolExecutor
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any, Coroutine, cast
from unittest.mock import patch

import grpc
import pytest
from google.protobuf import empty_pb2

from dex import (
    ApiKeyCredential,
    ApiKeyExchangeError,
    AsyncClient,
    BlobCache,
    Client,
    ClientOptions,
    DexServiceError,
)
from dex._credential_transport import (
    AsyncCredentialRunner,
    _AsyncCredentialInterceptor,
    _SyncCredentialInterceptor,
)
from dex._pull_worker import PullStream, PullWorkerRuntime
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc
from dex.flow import Registry
from dex.worker_options import PullWorkerOptions

API_KEY = "never-print-api-key"
TOKEN_A = "never-print-token-a"
TOKEN_B = "never-print-token-b"


def token_response(token: str, lifetime: int = 60) -> dict[str, object]:
    return {"access_token": token, "token_type": "Bearer", "expires_in": lifetime}


class TokenEndpoint:
    def __init__(self) -> None:
        self.responses: queue.Queue[tuple[int, object]] = queue.Queue()
        self.requests: list[tuple[str, str, dict[str, str], bytes]] = []
        self._lock = threading.Lock()
        self.request_received = threading.Event()
        self.response_allowed = threading.Event()
        self.response_allowed.set()
        self.drip_interval = 0.0
        endpoint = self

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self) -> None:  # noqa: N802
                body = self.rfile.read(int(self.headers["Content-Length"]))
                with endpoint._lock:
                    endpoint.requests.append(
                        (self.command, self.path, dict(self.headers.items()), body)
                    )
                    endpoint.request_received.set()
                endpoint.response_allowed.wait(3)
                status, payload = endpoint.responses.get(timeout=3)
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                if isinstance(payload, dict) and "redirect_to" in payload:
                    self.send_header("Location", str(payload["redirect_to"]))
                self.end_headers()
                try:
                    response_body = (
                        payload
                        if isinstance(payload, bytes)
                        else json.dumps(payload).encode()
                    )
                    if endpoint.drip_interval:
                        for byte in response_body:
                            self.wfile.write(bytes((byte,)))
                            self.wfile.flush()
                            time.sleep(endpoint.drip_interval)
                    else:
                        self.wfile.write(response_body)
                except (BrokenPipeError, ConnectionResetError):
                    pass

            def log_message(self, format: str, *args: object) -> None:
                pass

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.server.server_port}/token"
        self.thread = threading.Thread(target=self.server.serve_forever)
        self.thread.start()

    def close(self) -> None:
        self.response_allowed.set()
        self.server.shutdown()
        self.thread.join()
        self.server.server_close()


@pytest.fixture
def token_endpoint() -> Iterator[TokenEndpoint]:
    endpoint = TokenEndpoint()
    try:
        yield endpoint
    finally:
        endpoint.close()


def test_api_key_request_and_validation(token_endpoint: TokenEndpoint) -> None:
    endpoint = token_endpoint
    endpoint.responses.put(
        (200, {**token_response(TOKEN_A), "token_type": "bEaReR", "ignored": True})
    )
    credential = ApiKeyCredential(endpoint.url, "server-id", API_KEY)
    try:
        assert credential.token() == TOKEN_A
        assert credential.token() == TOKEN_A
        assert len(endpoint.requests) == 1
        method, path, headers, body = endpoint.requests[0]
        assert method == "POST"
        assert path == "/token"
        assert headers["Content-Type"] == "application/json"
        assert headers["Accept"] == "application/json"
        assert body == b'{"server_id":"server-id","api_key":"never-print-api-key"}'
    finally:
        credential.close()

    for url in (
        "",
        "not-a-url",
        "ftp://example.com/token",
        "http://example.com/token",
        "http://192.168.1.2/token",
        "http://localhost.example.com/token",
        "http://127.0.0.1.nip.io/token",
    ):
        with pytest.raises(ValueError):
            ApiKeyCredential(url, "server-id", API_KEY)
    for url in (
        "http://localhost/token",
        "http://127.1.2.3/token",
        "http://[::1]/token",
    ):
        ApiKeyCredential(url, "server-id", API_KEY).close()
    with pytest.raises(ValueError):
        ApiKeyCredential(endpoint.url, "", API_KEY)
    with pytest.raises(ValueError):
        ApiKeyCredential(endpoint.url, "server-id", "")
    with pytest.raises(ValueError):
        ApiKeyCredential(endpoint.url, "server-id", API_KEY, exchange_timeout=0)
    with pytest.raises(ValueError):
        ApiKeyCredential(
            endpoint.url, "server-id", API_KEY, exchange_timeout=float("nan")
        )
    with pytest.raises(ValueError):
        ApiKeyCredential(endpoint.url, " ", API_KEY)


@pytest.mark.parametrize(
    ("status", "payload"),
    [
        (201, token_response(TOKEN_A)),
        (200, b"not-json"),
        (200, []),
        (200, {"token_type": "Bearer", "expires_in": 10}),
        (200, {"access_token": "", "token_type": "Bearer", "expires_in": 10}),
        (200, {"access_token": 12, "token_type": "Bearer", "expires_in": 10}),
        (200, {"access_token": TOKEN_A, "expires_in": 10}),
        (200, {"access_token": TOKEN_A, "token_type": "Basic", "expires_in": 10}),
        (200, {"access_token": TOKEN_A, "token_type": "Bearer"}),
        (200, {"access_token": TOKEN_A, "token_type": "Bearer", "expires_in": 0}),
        (200, {"access_token": TOKEN_A, "token_type": "Bearer", "expires_in": True}),
        (200, {"access_token": TOKEN_A, "token_type": "Bearer", "expires_in": 1.5}),
    ],
)
def test_api_key_invalid_responses(
    token_endpoint: TokenEndpoint, status: int, payload: object
) -> None:
    token_endpoint.responses.put((status, payload))
    credential = ApiKeyCredential(token_endpoint.url, "server-id", API_KEY)
    try:
        with pytest.raises(ApiKeyExchangeError) as failure:
            credential.token()
        assert token_endpoint.url in str(failure.value)
        assert f"HTTP status {status}" in str(failure.value)
        assert API_KEY not in repr(failure.value)
        assert TOKEN_A not in repr(failure.value)
        assert API_KEY not in repr(credential)
        assert TOKEN_A not in repr(credential)
    finally:
        credential.close()


def test_api_key_endpoint_error_redacts_secrets(token_endpoint: TokenEndpoint) -> None:
    description = API_KEY + " " + TOKEN_A + " " + TOKEN_B + " " + "x" * 300
    token_endpoint.responses.put(
        (
            401,
            {
                "error": API_KEY,
                "error_description": description,
                "access_token": TOKEN_B,
            },
        )
    )
    credential = ApiKeyCredential(token_endpoint.url, "server-id", API_KEY)
    credential._token = TOKEN_A
    credential._expires_at = 0
    try:
        with pytest.raises(ApiKeyExchangeError) as failure:
            credential.token()
        message = str(failure.value)
        assert "HTTP status 401" in message
        assert "error=" in message and "error_description=" in message
        assert len(message.split("error_description=", 1)[1]) <= 200
        for value in (API_KEY, TOKEN_A, TOKEN_B, description):
            assert value not in message
            assert value not in repr(failure.value)
            assert value not in repr(credential)
    finally:
        credential.close()


def test_api_key_formatting_never_exposes_secrets(
    token_endpoint: TokenEndpoint,
) -> None:
    with pytest.raises(ValueError) as construction_failure:
        ApiKeyCredential("http://example.com/token", "server-id", API_KEY)
    credential = ApiKeyCredential(token_endpoint.url, "server-id", API_KEY)
    credential._token = TOKEN_A
    credential.close()
    with pytest.raises(RuntimeError) as closed_failure:
        credential.token()
    exchange_failure = ApiKeyExchangeError("token exchange failed")
    for value in (
        credential,
        construction_failure.value,
        closed_failure.value,
        exchange_failure,
    ):
        for formatted in (str(value), repr(value)):
            assert API_KEY not in formatted
            assert TOKEN_A not in formatted


def test_api_key_refresh_failure_and_expiry(token_endpoint: TokenEndpoint) -> None:
    token_endpoint.responses.put((200, token_response(TOKEN_A, 2)))
    token_endpoint.responses.put((503, {"error": "temporarily_unavailable"}))
    token_endpoint.responses.put((200, token_response(TOKEN_B, 2)))
    credential = ApiKeyCredential(token_endpoint.url, "server-id", API_KEY)
    try:
        assert credential.token() == TOKEN_A
        issued_at = credential._expires_at - 2
        credential._clock = lambda: issued_at + 1.61
        assert credential.token() == TOKEN_A
        refresh = credential._in_flight
        assert refresh is not None and refresh.event.wait(2)
        assert credential.token() == TOKEN_A
        assert len(token_endpoint.requests) == 2
        credential._clock = lambda: issued_at + 2.1
        assert credential.token() == TOKEN_B
        assert len(token_endpoint.requests) == 3
    finally:
        credential.close()


def test_api_key_failure_after_expiry(token_endpoint: TokenEndpoint) -> None:
    token_endpoint.responses.put((200, token_response(TOKEN_A, 2)))
    token_endpoint.responses.put((503, {"error": "unavailable"}))
    credential = ApiKeyCredential(token_endpoint.url, "server-id", API_KEY)
    try:
        assert credential.token() == TOKEN_A
        credential._clock = lambda: credential._expires_at + 0.1
        with pytest.raises(ApiKeyExchangeError, match="HTTP status 503"):
            credential.token()
        assert len(token_endpoint.requests) == 2
    finally:
        credential.close()


def test_api_key_transport_failure_and_timeout(token_endpoint: TokenEndpoint) -> None:
    unavailable = ApiKeyCredential("http://127.0.0.1:1/token", "server-id", API_KEY)
    with pytest.raises(ApiKeyExchangeError, match="HTTP status unavailable") as failure:
        unavailable.token()
    assert API_KEY not in str(failure.value)
    unavailable.close()

    token_endpoint.responses.put((200, token_response(TOKEN_A)))
    token_endpoint.response_allowed.clear()
    credential = ApiKeyCredential(
        token_endpoint.url, "server-id", API_KEY, exchange_timeout=0.1
    )
    try:
        with pytest.raises(ApiKeyExchangeError, match="HTTP status unavailable"):
            credential.token()
    finally:
        token_endpoint.response_allowed.set()
        credential.close()


@pytest.mark.parametrize(
    "failure",
    [
        http.client.BadStatusLine("invalid"),
        http.client.IncompleteRead(b"part"),
        RecursionError("nested"),
        RuntimeError(API_KEY),
    ],
)
def test_api_key_unexpected_exchange_failure_allows_retry(
    token_endpoint: TokenEndpoint, failure: Exception
) -> None:
    token_endpoint.responses.put((200, token_response(TOKEN_A)))
    credential = ApiKeyCredential(token_endpoint.url, "server-id", API_KEY)
    try:
        with patch.object(credential, "_exchange", side_effect=failure):
            with pytest.raises(ApiKeyExchangeError) as failed:
                credential.token()
            assert type(failure).__name__ in str(failed.value)
            assert API_KEY not in str(failed.value)
            assert credential._in_flight is None
        assert credential.token() == TOKEN_A
        assert len(token_endpoint.requests) == 1
    finally:
        credential.close()


def test_api_key_loopback_bypasses_http_proxy(
    token_endpoint: TokenEndpoint, monkeypatch: pytest.MonkeyPatch
) -> None:
    proxy = TokenEndpoint()
    monkeypatch.setenv("http_proxy", proxy.url)
    monkeypatch.setenv("HTTP_PROXY", proxy.url)
    monkeypatch.setenv("no_proxy", "")
    monkeypatch.setenv("NO_PROXY", "")
    token_endpoint.responses.put((200, token_response(TOKEN_A)))
    credential = ApiKeyCredential(token_endpoint.url, "server-id", API_KEY)
    try:
        assert credential.token() == TOKEN_A
        assert len(token_endpoint.requests) == 1
        assert proxy.requests == []
    finally:
        credential.close()
        proxy.close()


def test_api_key_https_uses_proxy_without_sending_key_in_connect(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    requests: list[bytes] = []

    class ProxyHandler(socketserver.BaseRequestHandler):
        def handle(self) -> None:
            requests.append(self.request.recv(4096))
            self.request.sendall(
                b"HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n"
            )

    proxy = socketserver.TCPServer(("127.0.0.1", 0), ProxyHandler)
    server_thread = threading.Thread(target=proxy.serve_forever)
    server_thread.start()
    monkeypatch.setenv("https_proxy", f"http://127.0.0.1:{proxy.server_address[1]}")
    monkeypatch.setenv("HTTPS_PROXY", f"http://127.0.0.1:{proxy.server_address[1]}")
    monkeypatch.setenv("no_proxy", "")
    monkeypatch.setenv("NO_PROXY", "")
    credential = ApiKeyCredential("https://example.test/token", "server-id", API_KEY)
    try:
        with pytest.raises(ApiKeyExchangeError):
            credential.token()
        assert requests and requests[0].startswith(b"CONNECT example.test:443 HTTP/1.")
        assert API_KEY.encode() not in requests[0]
    finally:
        credential.close()
        proxy.shutdown()
        server_thread.join()
        proxy.server_close()


@pytest.mark.parametrize("status", [307, 308])
def test_api_key_redirect_never_reposts_key(
    token_endpoint: TokenEndpoint, status: int
) -> None:
    target = TokenEndpoint()
    token_endpoint.responses.put((status, {"redirect_to": target.url}))
    credential = ApiKeyCredential(token_endpoint.url, "server-id", API_KEY)
    try:
        with pytest.raises(ApiKeyExchangeError, match=f"HTTP status {status}"):
            credential.token()
        assert len(token_endpoint.requests) == 1
        assert target.requests == []
    finally:
        credential.close()
        target.close()


def test_api_key_slow_response_has_one_deadline(token_endpoint: TokenEndpoint) -> None:
    token_endpoint.drip_interval = 0.07
    token_endpoint.responses.put((200, token_response(TOKEN_A)))
    credential = ApiKeyCredential(
        token_endpoint.url, "server-id", API_KEY, exchange_timeout=0.2
    )
    started_at = time.monotonic()
    try:
        with pytest.raises(ApiKeyExchangeError, match="exchange timed out"):
            credential.token()
        assert time.monotonic() - started_at < 0.35
        assert credential._in_flight is None
        token_endpoint.drip_interval = 0.0
        token_endpoint.responses.put((200, token_response(TOKEN_B)))
        assert credential.token() == TOKEN_B
        credential.close()
    finally:
        credential.close()


def test_api_key_delayed_response_cannot_publish_expired_token(
    token_endpoint: TokenEndpoint,
) -> None:
    token_endpoint.responses.put((200, token_response(TOKEN_A, 1)))
    token_endpoint.response_allowed.clear()
    credential = ApiKeyCredential(
        token_endpoint.url, "server-id", API_KEY, exchange_timeout=2
    )
    release_response = threading.Timer(1.1, token_endpoint.response_allowed.set)
    release_response.start()
    try:
        with pytest.raises(ApiKeyExchangeError, match="no valid token"):
            credential.token()
        assert credential._token == TOKEN_A
        assert credential._clock() >= credential._expires_at
    finally:
        release_response.join()
        credential.close()


def test_api_key_cached_token_returns_during_slow_refresh(
    token_endpoint: TokenEndpoint,
) -> None:
    token_endpoint.responses.put((200, token_response(TOKEN_A, 2)))
    token_endpoint.responses.put((200, token_response(TOKEN_B, 2)))
    credential = ApiKeyCredential(token_endpoint.url, "server-id", API_KEY)
    try:
        assert credential.token() == TOKEN_A
        issued_at = credential._expires_at - 2
        credential._clock = lambda: issued_at + 1.61
        token_endpoint.response_allowed.clear()
        token_endpoint.request_received.clear()
        assert credential.token() == TOKEN_A
        assert token_endpoint.request_received.wait(2)
        with ThreadPoolExecutor(max_workers=8) as executor:
            calls = [executor.submit(credential.token) for _ in range(8)]
            assert [call.result(timeout=0.5) for call in calls] == [TOKEN_A] * 8
        assert len(token_endpoint.requests) == 2
    finally:
        token_endpoint.response_allowed.set()
        credential.close()


def test_api_key_single_flight_and_rejected_token(
    token_endpoint: TokenEndpoint,
) -> None:
    token_endpoint.responses.put((200, token_response(TOKEN_A)))
    token_endpoint.responses.put((200, token_response(TOKEN_B)))
    token_endpoint.response_allowed.clear()
    credential = ApiKeyCredential(token_endpoint.url, "server-id", API_KEY)
    try:
        with ThreadPoolExecutor(max_workers=8) as executor:
            calls = [executor.submit(credential.token) for _ in range(8)]
            assert token_endpoint.request_received.wait(2)
            token_endpoint.response_allowed.set()
            assert [call.result(timeout=2) for call in calls] == [TOKEN_A] * 8
        assert len(token_endpoint.requests) == 1
        credential._reject_token(TOKEN_A)
        assert credential.token() == TOKEN_B
        credential._reject_token(TOKEN_A)
        assert credential.token() == TOKEN_B
        assert len(token_endpoint.requests) == 2
    finally:
        credential.close()


class RejectingFlowService(dex_pb2_grpc.FlowServiceServicer):
    def __init__(self, reject_all: bool = False) -> None:
        self.attempts: list[str | bytes | None] = []
        self.reject_all = reject_all

    def HealthCheck(  # noqa: N802
        self, request: empty_pb2.Empty, context: grpc.ServicerContext
    ) -> pb.HealthInfo:
        authorization = dict(context.invocation_metadata()).get("authorization")
        self.attempts.append(authorization)
        if authorization != f"Bearer {TOKEN_B}" or self.reject_all:
            context.abort(grpc.StatusCode.UNAUTHENTICATED, "rejected")
        return pb.HealthInfo()


@pytest.fixture
def rejecting_service() -> Iterator[tuple[str, RejectingFlowService]]:
    service = RejectingFlowService()
    server = grpc.server(ThreadPoolExecutor(max_workers=4))
    dex_pb2_grpc.add_FlowServiceServicer_to_server(service, server)  # type: ignore[no-untyped-call]
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    try:
        yield f"127.0.0.1:{port}", service
    finally:
        server.stop(grace=0).wait(timeout=3)


@pytest.mark.parametrize("reject_all", [False, True])
def test_api_key_client_retries_once(
    token_endpoint: TokenEndpoint,
    rejecting_service: tuple[str, RejectingFlowService],
    reject_all: bool,
) -> None:
    token_endpoint.responses.put((200, token_response(TOKEN_A)))
    token_endpoint.responses.put((200, token_response(TOKEN_B)))
    address, service = rejecting_service
    service.reject_all = reject_all
    credential = ApiKeyCredential(token_endpoint.url, "server-id", API_KEY)
    client = Client(
        Registry([]),
        cast(BlobCache, object()),
        ClientOptions(server_address=address, credential=credential),
    )
    try:
        if reject_all:
            with pytest.raises(DexServiceError) as failure:
                client.health_check()
            assert "rejected" in str(failure.value)
        else:
            assert client.health_check()
        assert service.attempts == [f"Bearer {TOKEN_A}", f"Bearer {TOKEN_B}"]
        assert len(token_endpoint.requests) == 2
    finally:
        client.close()
        credential.close()


def test_api_key_async_client_retries_once(
    token_endpoint: TokenEndpoint,
    rejecting_service: tuple[str, RejectingFlowService],
) -> None:
    token_endpoint.responses.put((200, token_response(TOKEN_A)))
    token_endpoint.responses.put((200, token_response(TOKEN_B)))
    address, service = rejecting_service
    credential = ApiKeyCredential(token_endpoint.url, "server-id", API_KEY)

    async def run() -> None:
        async with AsyncClient(
            Registry([]),
            cast(BlobCache, object()),
            ClientOptions(server_address=address, credential=credential),
        ) as client:
            assert await client.health_check()

    try:
        asyncio.run(run())
        assert service.attempts == [f"Bearer {TOKEN_A}", f"Bearer {TOKEN_B}"]
        assert len(token_endpoint.requests) == 2
    finally:
        credential.close()


def test_api_key_sync_retry_lookup_honors_rpc_deadline(
    token_endpoint: TokenEndpoint,
    rejecting_service: tuple[str, RejectingFlowService],
) -> None:
    token_endpoint.responses.put((200, token_response(TOKEN_A)))
    token_endpoint.responses.put((200, token_response(TOKEN_B)))
    credential = ApiKeyCredential(
        token_endpoint.url, "server-id", API_KEY, exchange_timeout=2
    )
    assert credential.token() == TOKEN_A
    token_endpoint.response_allowed.clear()
    address, service = rejecting_service
    channel = grpc.intercept_channel(
        grpc.insecure_channel(address), _SyncCredentialInterceptor(credential)
    )
    stub = dex_pb2_grpc.FlowServiceStub(channel)  # type: ignore[no-untyped-call]
    started_at = time.monotonic()
    try:
        with pytest.raises(grpc.RpcError) as failure:
            stub.HealthCheck(empty_pb2.Empty(), timeout=0.2)
        assert failure.value.code() == grpc.StatusCode.DEADLINE_EXCEEDED
        assert time.monotonic() - started_at < 0.6
        assert service.attempts == [f"Bearer {TOKEN_A}"]
    finally:
        token_endpoint.response_allowed.set()
        channel.close()
        credential.close()


def test_api_key_async_retry_lookup_honors_rpc_deadline(
    token_endpoint: TokenEndpoint,
    rejecting_service: tuple[str, RejectingFlowService],
) -> None:
    token_endpoint.responses.put((200, token_response(TOKEN_A)))
    token_endpoint.responses.put((200, token_response(TOKEN_B)))
    credential = ApiKeyCredential(
        token_endpoint.url, "server-id", API_KEY, exchange_timeout=2
    )
    assert credential.token() == TOKEN_A
    token_endpoint.response_allowed.clear()
    token_endpoint.request_received.clear()
    address, service = rejecting_service

    async def run() -> None:
        runner = AsyncCredentialRunner(credential)
        channel = grpc.aio.insecure_channel(
            address, interceptors=[_AsyncCredentialInterceptor(runner)]
        )
        stub = dex_pb2_grpc.FlowServiceStub(channel)  # type: ignore[no-untyped-call]
        try:
            started_at = time.monotonic()
            with pytest.raises(grpc.aio.AioRpcError) as failure:
                await stub.HealthCheck(empty_pb2.Empty(), timeout=0.2)
            assert failure.value.code() == grpc.StatusCode.DEADLINE_EXCEEDED
            assert time.monotonic() - started_at < 0.6
            assert service.attempts == [f"Bearer {TOKEN_A}"]

        finally:
            await channel.close()
            runner.close()

    try:
        asyncio.run(run())
    finally:
        token_endpoint.response_allowed.set()
        credential.close()


def test_api_key_async_retry_lookup_can_be_cancelled(
    token_endpoint: TokenEndpoint,
    rejecting_service: tuple[str, RejectingFlowService],
) -> None:
    token_endpoint.responses.put((200, token_response(TOKEN_A)))
    token_endpoint.responses.put((200, token_response(TOKEN_B)))
    credential = ApiKeyCredential(
        token_endpoint.url, "server-id", API_KEY, exchange_timeout=2
    )
    assert credential.token() == TOKEN_A
    token_endpoint.response_allowed.clear()
    token_endpoint.request_received.clear()
    address, service = rejecting_service

    async def run() -> None:
        runner = AsyncCredentialRunner(credential)
        channel = grpc.aio.insecure_channel(
            address, interceptors=[_AsyncCredentialInterceptor(runner)]
        )
        stub = dex_pb2_grpc.FlowServiceStub(channel)  # type: ignore[no-untyped-call]

        async def make_call() -> None:
            await stub.HealthCheck(empty_pb2.Empty(), timeout=5)

        try:
            call = asyncio.create_task(make_call())
            assert await asyncio.to_thread(token_endpoint.request_received.wait, 2)
            started_at = time.monotonic()
            call.cancel()
            with pytest.raises(asyncio.CancelledError):
                await call
            assert time.monotonic() - started_at < 0.3
            assert service.attempts == [f"Bearer {TOKEN_A}"]
        finally:
            await channel.close()
            runner.close()

    try:
        asyncio.run(run())
    finally:
        token_endpoint.response_allowed.set()
        credential.close()


class RejectedPollError(grpc.RpcError):
    def code(self) -> grpc.StatusCode:
        return grpc.StatusCode.UNAUTHENTICATED


class RejectedPollCall:
    async def write(self, request: pb.PollWorkerTaskRequest) -> None:
        pass

    async def read(self) -> pb.PollWorkerTaskResponse:
        raise RejectedPollError()

    def cancel(self) -> None:
        pass


class RejectingPollStub:
    def __init__(self) -> None:
        self.metadata: list[Any] = []

    def PollWorkerTask(self, *, metadata: Any) -> RejectedPollCall:  # noqa: N802
        self.metadata.append(metadata)
        return RejectedPollCall()


def test_api_key_poll_retries_immediately_then_uses_permanent_backoff(
    token_endpoint: TokenEndpoint,
) -> None:
    token_endpoint.responses.put((200, token_response(TOKEN_A)))
    token_endpoint.responses.put((200, token_response(TOKEN_B)))
    credential = ApiKeyCredential(token_endpoint.url, "server-id", API_KEY)

    async def run() -> None:
        runner = AsyncCredentialRunner(credential)
        runtime = PullWorkerRuntime(
            Registry([]),
            None,
            PullWorkerOptions(app_worker_id="api-key-poll-worker"),
            "unused",
            None,
            credential_runner=runner,
        )
        stopping = asyncio.Event()
        runtime._stopping = stopping
        stub = RejectingPollStub()
        stream = PullStream(runtime, stub, "api-key-poll-worker")
        backoffs: list[float] = []

        async def capture_backoff(
            awaitable: Coroutine[Any, Any, bool], *, timeout: float
        ) -> bool:
            awaitable.close()
            backoffs.append(timeout)
            stopping.set()
            raise asyncio.TimeoutError

        try:
            with patch("dex._pull_worker.asyncio.wait_for", new=capture_backoff), patch(
                "dex._pull_worker.random.uniform", return_value=0
            ):
                await stream.run()
            assert stub.metadata == [
                (("authorization", f"Bearer {TOKEN_A}"),),
                (("authorization", f"Bearer {TOKEN_B}"),),
            ]
            assert backoffs == [60]
            assert credential._token == ""
            assert len(token_endpoint.requests) == 2
            assert (
                runtime.registration.snapshot().queues[0].last_permanent_error
                is not None
            )
        finally:
            runner.close()

    try:
        asyncio.run(run())
    finally:
        credential.close()
