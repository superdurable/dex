# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

import asyncio
import hashlib
import json
import os
import secrets
import socket
import subprocess
import threading
import time
from collections.abc import Iterator
from concurrent.futures import ThreadPoolExecutor
from contextlib import ExitStack
from dataclasses import dataclass
from datetime import timedelta
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from importlib.resources import files
from unittest.mock import patch
from uuid import uuid4
from typing import cast

import grpc
import pytest
from google.protobuf.empty_pb2 import Empty

from dex import (
    ApiKeyCredential,
    AsyncClient,
    AsyncWorker,
    BlobCache,
    BlobCacheConfig,
    Client,
    ClientOptions,
    Context,
    DexServiceError,
    FileCredential,
    Flow,
    FlowServiceTlsOptions,
    PullWorkerOptions,
    Registry,
    Step,
    StepDecision,
    StepList,
    Worker,
    WorkerOptions,
    graceful_complete,
    open_blob_cache,
)
from dex.dexpb import dex_pb2_grpc

from .tls_transport import TlsCertificates, TlsProxy, create_certificates


class TlsTransportStep(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        return graceful_complete(input)


class TlsTransportFlow(Flow[str]):
    def get_steps(self) -> StepList[str]:
        return StepList.start_step(TlsTransportStep())


@dataclass(frozen=True)
class TlsEnvironment:
    address: str
    proxy: TlsProxy
    certificates: TlsCertificates
    credential: FileCredential
    cache: BlobCache


@pytest.fixture(scope="module")
def environment(tmp_path_factory: pytest.TempPathFactory) -> Iterator[TlsEnvironment]:
    binary = os.environ.get("DEX_SERVER_ZD_BINARY")
    if not binary:
        raise RuntimeError(
            "DEX_SERVER_ZD_BINARY must name a dex-server-zd binary; run ./run-integration-tests.sh"
        )
    directory = tmp_path_factory.mktemp("dex-python-tls")
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    address = f"127.0.0.1:{port}"
    token = secrets.token_hex(24)
    token_file = directory / "token"
    token_file.write_text(token)
    token_hash_file = directory / "token-hashes.yaml"
    token_hash_file.write_text(
        f"tokens:\n  - sha256: {hashlib.sha256(token.encode()).hexdigest()}\n"
    )
    config_file = directory / "server.yaml"
    config_file.write_text(
        "auth:\n  authenticators:\n    - kind: staticTokens\n"
        f"      file: {json.dumps(str(token_hash_file))}\n"
        "dev:\n  durabilityMode: processCrashSafe\n"
    )
    certificates = create_certificates(directory / "primary", has_ip_sans=False)
    credential = FileCredential(token_file)
    with ExitStack() as cleanup:
        cache = open_blob_cache(
            BlobCacheConfig(str(directory / "cache"), 64 * 1024 * 1024)
        )
        cleanup.callback(cache.close)
        log = cleanup.enter_context((directory / "server.log").open("wb"))
        process = subprocess.Popen(
            [
                binary,
                "dev",
                "--data-dir",
                str(directory / "zd"),
                "--listen",
                address,
                "--config",
                str(config_file),
            ],
            stdin=subprocess.DEVNULL,
            stdout=log,
            stderr=subprocess.STDOUT,
        )
        cleanup.callback(_stop_server, process)
        with grpc.insecure_channel(address) as channel:
            service = dex_pb2_grpc.FlowServiceStub(channel)  # type: ignore[no-untyped-call]
            deadline = time.monotonic() + 60
            while True:
                try:
                    service.GetServerInfo(
                        Empty(),
                        timeout=1,
                        metadata=(("authorization", f"Bearer {token}"),),
                    )
                    break
                except grpc.RpcError:
                    if process.poll() is not None or time.monotonic() >= deadline:
                        raise RuntimeError(
                            "Authenticated server-zd did not become ready:\n"
                            + (directory / "server.log").read_text()
                        ) from None
                    threading.Event().wait(0.05)
        proxy = cleanup.enter_context(TlsProxy(certificates, address))
        yield TlsEnvironment(address, proxy, certificates, credential, cache)


def _stop_server(process: subprocess.Popen[bytes]) -> None:
    if process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=10)


@pytest.mark.parametrize("target", ["host", "dns", "override", "plaintext"])
def test_sync_client_and_pull_worker_complete_flow(
    environment: TlsEnvironment, target: str
) -> None:
    environment.proxy.server_names.clear()
    environment.proxy.handshakes.clear()
    flow = TlsTransportFlow()
    registry = Registry([flow])
    tls = FlowServiceTlsOptions(environment.certificates.root_pem)
    address = environment.proxy.address
    if target == "dns":
        address = f"DNS:///{address}"
    elif target == "override":
        address = f"127.0.0.1:{environment.proxy.port}"
        tls = FlowServiceTlsOptions(environment.certificates.root_pem, "localhost")
    elif target == "plaintext":
        address = environment.address
    options = None if target == "plaintext" else tls
    with Worker(
        registry,
        environment.cache,
        WorkerOptions(
            server_address=address,
            flow_service_tls=options,
            credential=environment.credential,
            pull=PullWorkerOptions(max_concurrent_tasks=2),
        ),
    ) as worker, Client(
        registry,
        environment.cache,
        ClientOptions(
            server_address=address,
            flow_service_tls=options,
            credential=environment.credential,
        ),
    ) as client, ThreadPoolExecutor(
        max_workers=1
    ) as executor:
        started = executor.submit(worker.start)
        try:
            worker.wait_pull_registered(timeout=30)
            flow_id = f"tls-sync-{uuid4()}"
            value = "tls-value-" * 20000
            client.start_flow(flow, flow_id, value)
            assert (
                client.wait_for_flow(flow_id, timedelta(seconds=30)).single_output(str)
                == value
            )
        finally:
            worker.stop()
            started.result(timeout=10)
    if target != "plaintext":
        assert ("TLSv1.3", "h2") in environment.proxy.handshakes or (
            "TLSv1.2",
            "h2",
        ) in environment.proxy.handshakes
        assert environment.proxy.server_names
        assert set(environment.proxy.server_names) == {"localhost"}


def test_async_client_and_pull_worker_complete_flow(
    environment: TlsEnvironment,
) -> None:
    asyncio.run(_check_async_flow(environment))


async def _check_async_flow(environment: TlsEnvironment) -> None:
    flow = TlsTransportFlow()
    registry = Registry([flow])
    tls = FlowServiceTlsOptions(environment.certificates.root_pem)
    async with AsyncWorker(
        registry,
        environment.cache,
        WorkerOptions(
            server_address=environment.proxy.address,
            flow_service_tls=tls,
            credential=environment.credential,
            pull=PullWorkerOptions(max_concurrent_tasks=2),
        ),
    ) as worker, AsyncClient(
        registry,
        environment.cache,
        ClientOptions(
            server_address=environment.proxy.address,
            flow_service_tls=tls,
            credential=environment.credential,
        ),
    ) as client:
        started = asyncio.create_task(worker.start())
        try:
            await worker.wait_pull_registered(timeout=30)
            flow_id = f"tls-async-{uuid4()}"
            value = "async-tls-value-" * 20000
            await client.start_flow(flow, flow_id, value)
            result = await client.wait_for_flow(flow_id, timedelta(seconds=30))
            assert result.single_output(str) == value
        finally:
            await worker.stop()
            await asyncio.wait_for(started, 10)


def test_tls_without_bearer_token_is_rejected(environment: TlsEnvironment) -> None:
    flow = TlsTransportFlow()
    with Client(
        Registry([flow]),
        environment.cache,
        ClientOptions(
            server_address=environment.proxy.address,
            flow_service_tls=FlowServiceTlsOptions(environment.certificates.root_pem),
        ),
    ) as client:
        with pytest.raises(DexServiceError) as failure:
            client.start_flow(flow, f"tls-unauthorized-{uuid4()}", "value")
        assert failure.value.code is grpc.StatusCode.UNAUTHENTICATED


@pytest.mark.parametrize(
    "failure_case", ["wrong-name", "untrusted", "plaintext-to-tls", "missing-override"]
)
def test_tls_rejects_invalid_transport(
    environment: TlsEnvironment, failure_case: str
) -> None:
    tls: FlowServiceTlsOptions | None = FlowServiceTlsOptions()
    if failure_case == "wrong-name":
        tls = FlowServiceTlsOptions(environment.certificates.root_pem, "wrong.example")
    elif failure_case == "plaintext-to-tls":
        tls = None
    address = environment.proxy.address
    if failure_case == "missing-override":
        address = f"127.0.0.1:{environment.proxy.port}"
        tls = FlowServiceTlsOptions(environment.certificates.root_pem)
    transport_client = Client(
        Registry([]),
        environment.cache,
        ClientOptions(
            server_address=address,
            flow_service_tls=tls,
            credential=environment.credential,
        ),
    )
    with transport_client, pytest.raises(grpc.RpcError) as failure:
        transport_client._service.GetServerInfo(Empty(), timeout=2)
    assert failure.value.code() is grpc.StatusCode.UNAVAILABLE
    detail = failure.value.details() or ""
    if failure_case == "wrong-name":
        assert "hostname" in detail.lower() or "wrong.example" in detail
    elif failure_case == "untrusted":
        assert "CERTIFICATE_VERIFY_FAILED" in detail


def test_wrong_server_name_fails_worker_start(environment: TlsEnvironment) -> None:
    with Worker(
        Registry([]),
        environment.cache,
        WorkerOptions(
            server_address=environment.proxy.address,
            flow_service_tls=FlowServiceTlsOptions(
                environment.certificates.root_pem, "wrong.example"
            ),
            credential=environment.credential,
            attribute_index_sync_timeout=timedelta(seconds=2),
            pull=PullWorkerOptions(max_concurrent_tasks=2),
        ),
    ) as worker, pytest.raises(RuntimeError, match="GetServerInfo failed") as failure:
        worker.start()
    assert "hostname" in str(failure.value).lower() or "wrong.example" in str(
        failure.value
    )


@pytest.mark.parametrize("failure_case", ["wrong-name", "untrusted"])
def test_async_tls_rejects_invalid_transport(
    environment: TlsEnvironment, failure_case: str
) -> None:
    asyncio.run(_check_async_invalid_transport(environment, failure_case))


async def _check_async_invalid_transport(
    environment: TlsEnvironment, failure_case: str
) -> None:
    tls = (
        FlowServiceTlsOptions(environment.certificates.root_pem, "wrong.example")
        if failure_case == "wrong-name"
        else FlowServiceTlsOptions()
    )
    async with AsyncClient(
        Registry([]),
        environment.cache,
        ClientOptions(
            server_address=environment.proxy.address,
            flow_service_tls=tls,
            credential=environment.credential,
        ),
    ) as client:
        with pytest.raises(grpc.aio.AioRpcError) as failure:
            await client._service.GetServerInfo(Empty(), timeout=2)
        assert failure.value.code() is grpc.StatusCode.UNAVAILABLE
        detail = failure.value.details() or ""
        if failure_case == "wrong-name":
            assert "hostname" in detail.lower() or "wrong.example" in detail
        else:
            assert "CERTIFICATE_VERIFY_FAILED" in detail
    async with AsyncWorker(
        Registry([]),
        environment.cache,
        WorkerOptions(
            server_address=environment.proxy.address,
            flow_service_tls=tls,
            credential=environment.credential,
            attribute_index_sync_timeout=timedelta(seconds=2),
            pull=PullWorkerOptions(max_concurrent_tasks=2),
        ),
    ) as worker:
        with pytest.raises(RuntimeError, match="GetServerInfo failed") as startup:
            await worker.start()
        if failure_case == "wrong-name":
            assert "hostname" in str(startup.value).lower() or "wrong.example" in str(
                startup.value
            )
        else:
            assert "CERTIFICATE_VERIFY_FAILED" in str(startup.value)


def test_extra_root_does_not_replace_configured_default(
    environment: TlsEnvironment, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    extra = create_certificates(tmp_path / "extra")
    system = create_certificates(tmp_path / "system")
    root_file = tmp_path / "default.pem"
    root_file.write_bytes(environment.certificates.root_pem)
    system_directory = tmp_path / "system-directory"
    system_directory.mkdir()
    (system_directory / "12345678.0").write_bytes(system.root_pem)
    monkeypatch.setenv("SSL_CERT_FILE", str(system.certificate_file.parent / "ca.pem"))
    monkeypatch.setenv("SSL_CERT_DIR", str(system_directory))
    monkeypatch.setenv("GRPC_DEFAULT_SSL_ROOTS_FILE_PATH", str(root_file))
    with patch(
        "grpc.ssl_channel_credentials", wraps=grpc.ssl_channel_credentials
    ) as credentials:
        with Client(
            Registry([]),
            environment.cache,
            ClientOptions(
                server_address=environment.proxy.address,
                flow_service_tls=FlowServiceTlsOptions(extra.root_pem),
                credential=environment.credential,
            ),
        ) as client:
            assert client.health_check()
    bundle = cast(bytes, credentials.call_args.args[0])
    assert (
        bundle
        == environment.certificates.root_pem.strip() + b"\n" + extra.root_pem.strip()
    )
    assert system.root_pem.strip() not in bundle
    assert (
        files("grpc").joinpath("_cython", "_credentials", "roots.pem").read_bytes()
        not in bundle
    )
    with TlsProxy(extra, environment.address) as extra_proxy, Client(
        Registry([]),
        environment.cache,
        ClientOptions(
            server_address=extra_proxy.address,
            flow_service_tls=FlowServiceTlsOptions(extra.root_pem),
            credential=environment.credential,
        ),
    ) as client:
        assert client.health_check()
    with TlsProxy(system, environment.address) as system_proxy, Client(
        Registry([]),
        environment.cache,
        ClientOptions(
            server_address=system_proxy.address,
            flow_service_tls=FlowServiceTlsOptions(extra.root_pem),
            credential=environment.credential,
        ),
    ) as client:
        with pytest.raises(grpc.RpcError) as failure:
            client._service.GetServerInfo(Empty(), timeout=2)
        assert failure.value.code() is grpc.StatusCode.UNAVAILABLE
        assert "CERTIFICATE_VERIFY_FAILED" in (failure.value.details() or "")


class TokenExchangeServer(ThreadingHTTPServer):
    def __init__(self, token: str) -> None:
        super().__init__(("127.0.0.1", 0), TokenExchangeEndpoint)
        self.token = token


class TokenExchangeEndpoint(BaseHTTPRequestHandler):
    def do_POST(self) -> None:  # noqa: N802
        self.rfile.read(int(self.headers["Content-Length"]))
        response = json.dumps(
            {
                "access_token": cast(TokenExchangeServer, self.server).token,
                "token_type": "Bearer",
                "expires_in": 3600,
            }
        ).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(response)))
        self.end_headers()
        self.wfile.write(response)

    def log_message(self, format: str, *args: object) -> None:
        pass


def test_api_key_bearer_credential_composes_with_tls(
    environment: TlsEnvironment,
) -> None:
    server = TokenExchangeServer(environment.credential.token())
    thread = threading.Thread(
        target=server.serve_forever, name="dex-test-token-exchange"
    )
    thread.start()
    credential = ApiKeyCredential(
        f"http://127.0.0.1:{server.server_port}/token", "dex-test", "test-api-key"
    )
    try:
        with Client(
            Registry([]),
            environment.cache,
            ClientOptions(
                server_address=environment.proxy.address,
                flow_service_tls=FlowServiceTlsOptions(
                    environment.certificates.root_pem
                ),
                credential=credential,
            ),
        ) as client:
            assert client.health_check()
    finally:
        credential.close()
        server.shutdown()
        server.server_close()
        thread.join(timeout=10)
        assert not thread.is_alive()
