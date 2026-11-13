# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A remote-style Dex server for tests: a bearer-token server-zd, a token endpoint, a TLS proxy."""

from __future__ import annotations

import contextlib
import hashlib
import json
import secrets
import socket
import ssl
import subprocess
import threading
import time
from collections.abc import Iterator
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

from dex import BlobCacheConfig, Client, ClientOptions, FlowNotFoundError, Registry, open_blob_cache

from dex_agents.testing import find_free_ports, resolve_dexcli_path, resolve_server_zd_path

#: The server ID the token endpoint scopes its access tokens to.
TEST_SERVER_ID = "dex-test"
_READY_LINE = "dex-server-zd serving"
_READY_SECONDS = 60.0


class _FixedToken:
    def __init__(self, token: str) -> None:
        self._token = token

    def token(self) -> str:
        return self._token


@dataclass
class TokenEndpoint:
    """A loopback token endpoint that issues the server's token for one API key.

    Attributes:
        api_key: The only key it exchanges.
        access_token: The token it issues.
        exchange_count: How many exchanges it answered with a token.
    """

    api_key: str
    access_token: str
    exchange_count: int = 0
    rejected_count: int = 0
    _server: ThreadingHTTPServer | None = None
    _lock: threading.Lock = field(default_factory=threading.Lock)

    @property
    def url(self) -> str:
        """Return the token URL."""
        assert self._server is not None, "start the endpoint first"
        return f"http://127.0.0.1:{self._server.server_address[1]}/token"

    def start(self) -> None:
        """Serve on a free loopback port in a thread."""
        endpoint = self

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self) -> None:
                length = int(self.headers.get("content-length", "0"))
                body: dict[str, Any] = json.loads(self.rfile.read(length) or b"{}")
                is_accepted = (
                    body.get("api_key") == endpoint.api_key
                    and body.get("server_id") == TEST_SERVER_ID
                )
                with endpoint._lock:
                    if is_accepted:
                        endpoint.exchange_count += 1
                    else:
                        endpoint.rejected_count += 1
                answer: dict[str, Any] = (
                    {
                        "access_token": endpoint.access_token,
                        "token_type": "Bearer",
                        "expires_in": 3_600,
                    }
                    if is_accepted
                    else {"error": "invalid_api_key"}
                )
                encoded = json.dumps(answer).encode()
                self.send_response(200 if is_accepted else 401)
                self.send_header("content-type", "application/json")
                self.send_header("content-length", str(len(encoded)))
                self.end_headers()
                self.wfile.write(encoded)

            def log_message(self, format: str, *args: Any) -> None:
                return

        self._server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=self._server.serve_forever, daemon=True).start()

    def stop(self) -> None:
        """Stop serving."""
        if self._server is not None:
            self._server.shutdown()
            self._server.server_close()


@dataclass(frozen=True)
class CertificateFiles:
    """A test CA and a server certificate for localhost and 127.0.0.1 that it signed."""

    ca_file: Path
    certificate_file: Path
    key_file: Path


def create_test_certificates(directory: Path) -> CertificateFiles:
    """Issue the certificates with the openssl CLI, valid for one day."""
    directory.mkdir(parents=True, exist_ok=True)
    extensions = directory / "extensions.cnf"
    extensions.write_text(
        "[dex_test_ca]\nbasicConstraints = critical, CA:TRUE\nkeyUsage = critical, keyCertSign\n"
        "[dex_test_server]\nbasicConstraints = CA:FALSE\n"
        "keyUsage = critical, digitalSignature\nextendedKeyUsage = serverAuth\n"
        "subjectAltName = DNS:localhost, IP:127.0.0.1\n"
    )

    def openssl(*arguments: str) -> None:
        subprocess.run(["openssl", *arguments], check=True, capture_output=True)

    ca_key, ca_csr, ca_file = (directory / name for name in ("ca.key", "ca.csr", "ca.pem"))
    key_file, csr, certificate_file = (
        directory / name for name in ("server.key", "server.csr", "server.pem")
    )
    openssl("ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", str(ca_key))
    openssl("req", "-new", "-key", str(ca_key), "-subj", "/CN=Dex CLI test CA", "-out", str(ca_csr))
    openssl(
        "x509", "-req", "-in", str(ca_csr), "-signkey", str(ca_key), "-days", "1",
        "-extfile", str(extensions), "-extensions", "dex_test_ca", "-out", str(ca_file),
    )  # fmt: skip
    openssl("ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", str(key_file))
    openssl("req", "-new", "-key", str(key_file), "-subj", "/CN=localhost", "-out", str(csr))
    openssl(
        "x509", "-req", "-in", str(csr), "-CA", str(ca_file), "-CAkey", str(ca_key),
        "-set_serial", "2", "-days", "1", "-extfile", str(extensions),
        "-extensions", "dex_test_server", "-out", str(certificate_file),
    )  # fmt: skip
    return CertificateFiles(ca_file, certificate_file, key_file)


class TlsTerminatingProxy:
    """Accept TLS with ALPN h2 on loopback and forward the plaintext bytes to a port."""

    def __init__(self, certificates: CertificateFiles, upstream_port: int) -> None:
        """Bind a free loopback port; ``start`` serves it."""
        self._context = ssl.create_default_context(ssl.Purpose.CLIENT_AUTH)
        self._context.load_cert_chain(certificates.certificate_file, certificates.key_file)
        self._context.set_alpn_protocols(["h2"])
        self._upstream_port = upstream_port
        self._listener = socket.create_server(("127.0.0.1", 0))
        self.handshake_count = 0
        self._is_stopped = False

    @property
    def port(self) -> int:
        """Return the TLS port."""
        return int(self._listener.getsockname()[1])

    def start(self) -> None:
        """Accept connections in a thread."""
        threading.Thread(target=self._accept, daemon=True).start()

    def stop(self) -> None:
        """Stop accepting."""
        self._is_stopped = True
        self._listener.close()

    def _accept(self) -> None:
        while not self._is_stopped:
            try:
                downstream, _ = self._listener.accept()
            except OSError:
                return
            threading.Thread(target=self._serve, args=(downstream,), daemon=True).start()

    def _serve(self, downstream: socket.socket) -> None:
        try:
            secured = self._context.wrap_socket(downstream, server_side=True)
        except (OSError, ssl.SSLError):
            downstream.close()
            return
        self.handshake_count += 1
        upstream = socket.create_connection(("127.0.0.1", self._upstream_port))
        threading.Thread(target=_pipe, args=(upstream, secured), daemon=True).start()
        _pipe(secured, upstream)


def _pipe(source: socket.socket, target: socket.socket) -> None:
    try:
        while chunk := source.recv(65536):
            target.sendall(chunk)
    except OSError:
        pass
    finally:
        for end in (source, target):
            with contextlib.suppress(OSError):
                end.shutdown(socket.SHUT_RDWR)


@dataclass
class AuthenticatedDexServer:
    """A ``dex-server-zd dev`` that takes only one bearer token, with its token endpoint and proxy.

    Attributes:
        port: The plaintext FlowService port.
        access_token: The one token the server accepts.
        token_endpoint: Exchanges ``api_key`` for ``access_token``.
        proxy: TLS on ``localhost:<proxy.port>``, signed by ``certificates.ca_file``.
    """

    port: int
    access_token: str
    api_key: str
    token_endpoint: TokenEndpoint
    certificates: CertificateFiles
    proxy: TlsTerminatingProxy
    process: subprocess.Popen[bytes]
    log_path: Path

    @property
    def address(self) -> str:
        """Return the plaintext ``host:port``."""
        return f"127.0.0.1:{self.port}"

    def stop(self) -> None:
        """Stop the proxy, the token endpoint, and the server."""
        self.proxy.stop()
        self.token_endpoint.stop()
        self.process.terminate()
        try:
            self.process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            self.process.kill()
            self.process.wait()


@contextlib.contextmanager
def authenticated_dex_server(directory: Path) -> Iterator[AuthenticatedDexServer]:
    """Run the server, its token endpoint, and its TLS proxy until the block ends."""
    directory.mkdir(parents=True, exist_ok=True)
    access_token = secrets.token_hex(24)
    api_key = f"dex_test_key_{secrets.token_hex(8)}"
    hashes = directory / "token-hashes.yaml"
    digest = hashlib.sha256(access_token.encode()).hexdigest()
    hashes.write_text(f"tokens:\n  - sha256: {digest}\n")
    config = directory / "server-zd.yaml"
    config.write_text(
        "auth:\n  authenticators:\n    - kind: staticTokens\n"
        f"      file: {json.dumps(str(hashes))}\n"
        "dev:\n  durabilityMode: processCrashSafe\n"
    )
    (port,) = find_free_ports(1)
    log_path = directory / "server-zd.log"
    with log_path.open("wb") as log:
        process = subprocess.Popen(
            [
                str(resolve_server_zd_path(resolve_dexcli_path())),
                "dev",
                "--data-dir",
                str(directory / "zd"),
                "--listen",
                f"127.0.0.1:{port}",
                "--config",
                str(config),
            ],  # fmt: skip
            stdout=log,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
    token_endpoint = TokenEndpoint(api_key, access_token)
    token_endpoint.start()
    certificates = create_test_certificates(directory / "certificates")
    proxy = TlsTerminatingProxy(certificates, port)
    proxy.start()
    server = AuthenticatedDexServer(
        port, access_token, api_key, token_endpoint, certificates, proxy, process, log_path
    )
    try:
        _wait_until_serving(server, directory / "cache")
        yield server
    finally:
        server.stop()


def _wait_until_serving(server: AuthenticatedDexServer, cache_directory: Path) -> None:
    """Wait for the ready line, then for a real authenticated call."""
    deadline = time.monotonic() + _READY_SECONDS
    while _READY_LINE not in server.log_path.read_text(errors="replace"):
        if server.process.poll() is not None or time.monotonic() > deadline:
            raise AssertionError(f"dex-server-zd did not serve:\n{server.log_path.read_text()}")
        time.sleep(0.1)
    cache_directory.mkdir(parents=True, exist_ok=True)
    blob_cache = open_blob_cache(BlobCacheConfig(str(cache_directory), 1024 * 1024))
    client = Client(
        Registry([]),
        blob_cache,
        ClientOptions(server_address=server.address, credential=_FixedToken(server.access_token)),
    )
    try:
        while True:
            try:
                client.describe_flow("dex-cli-test-readiness-probe")
            except FlowNotFoundError:
                return
            except Exception:
                if time.monotonic() > deadline:
                    raise
                time.sleep(0.1)
    finally:
        client.close()
        blob_cache.close()
