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
import ssl
import subprocess
import threading
from dataclasses import dataclass
from pathlib import Path
from types import TracebackType


@dataclass(frozen=True)
class TlsCertificates:
    root_pem: bytes
    certificate_file: Path
    private_key_file: Path


def create_certificates(directory: Path) -> TlsCertificates:
    directory.mkdir()
    extensions = directory / "extensions.cnf"
    extensions.write_text(
        "[ca]\nbasicConstraints=critical,CA:TRUE\nkeyUsage=critical,keyCertSign\n"
        "[server]\nbasicConstraints=CA:FALSE\nkeyUsage=critical,digitalSignature\n"
        "extendedKeyUsage=serverAuth\nsubjectAltName=DNS:localhost,IP:127.0.0.1,IP:::1\n"
    )
    commands = [
        ["ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", "ca.key"],
        [
            "req",
            "-new",
            "-key",
            "ca.key",
            "-subj",
            f"/CN=Dex test CA {directory.name}",
            "-out",
            "ca.csr",
        ],
        [
            "x509",
            "-req",
            "-in",
            "ca.csr",
            "-signkey",
            "ca.key",
            "-days",
            "1",
            "-extfile",
            "extensions.cnf",
            "-extensions",
            "ca",
            "-out",
            "ca.pem",
        ],
        ["ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", "server.key"],
        [
            "req",
            "-new",
            "-key",
            "server.key",
            "-subj",
            "/CN=localhost",
            "-out",
            "server.csr",
        ],
        [
            "x509",
            "-req",
            "-in",
            "server.csr",
            "-CA",
            "ca.pem",
            "-CAkey",
            "ca.key",
            "-set_serial",
            "2",
            "-days",
            "1",
            "-extfile",
            "extensions.cnf",
            "-extensions",
            "server",
            "-out",
            "server.pem",
        ],
    ]
    for arguments in commands:
        subprocess.run(
            ["openssl", *arguments], cwd=directory, check=True, capture_output=True
        )
    return TlsCertificates(
        (directory / "ca.pem").read_bytes(),
        directory / "server.pem",
        directory / "server.key",
    )


class TlsProxy:
    def __init__(
        self,
        certificates: TlsCertificates,
        upstream_address: str,
        alpn_protocols: tuple[str, ...] = ("h2",),
        maximum_version: ssl.TLSVersion = ssl.TLSVersion.MAXIMUM_SUPPORTED,
        minimum_version: ssl.TLSVersion = ssl.TLSVersion.TLSv1_2,
    ) -> None:
        self._context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        self._context.load_cert_chain(
            certificates.certificate_file, certificates.private_key_file
        )
        self._context.minimum_version = minimum_version
        self._context.maximum_version = maximum_version
        if maximum_version == ssl.TLSVersion.TLSv1_1:
            self._context.set_ciphers("DEFAULT:@SECLEVEL=0")
        self._context.set_alpn_protocols(list(alpn_protocols))
        self.server_names: list[str | None] = []
        self._context.set_servername_callback(self._record_server_name)
        host, _, port = upstream_address.rpartition(":")
        self._upstream_host = host
        self._upstream_port = int(port)
        self.handshakes: list[tuple[str | None, str | None]] = []
        self._connections: set[asyncio.Task[None]] = set()
        self._ready = threading.Event()
        self._loop: asyncio.AbstractEventLoop | None = None
        self._stopping: asyncio.Event | None = None
        self._failure: BaseException | None = None
        self.port = 0
        self._thread = threading.Thread(target=self._run, name="dex-test-tls-proxy")

    @property
    def address(self) -> str:
        return f"localhost:{self.port}"

    def __enter__(self) -> TlsProxy:
        self._thread.start()
        if not self._ready.wait(10):
            raise RuntimeError("TLS proxy did not start")
        if self._failure is not None:
            raise RuntimeError("TLS proxy failed") from self._failure
        return self

    def __exit__(
        self,
        exception_type: type[BaseException] | None,
        exception: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        assert self._loop is not None and self._stopping is not None
        self._loop.call_soon_threadsafe(self._stopping.set)
        self._thread.join(timeout=10)
        assert not self._thread.is_alive(), "TLS proxy did not stop"
        if self._failure is not None:
            raise RuntimeError("TLS proxy failed") from self._failure

    def _run(self) -> None:
        try:
            asyncio.run(self._serve())
        except BaseException as failure:
            self._failure = failure
            self._ready.set()

    async def _serve(self) -> None:
        self._loop = asyncio.get_running_loop()
        self._stopping = asyncio.Event()
        server = await asyncio.start_server(
            self._accept,
            "127.0.0.1",
            0,
            ssl=self._context,
            ssl_handshake_timeout=2,
            ssl_shutdown_timeout=1,
        )
        async with server:
            self.port = server.sockets[0].getsockname()[1]
            self._ready.set()
            await self._stopping.wait()
            server.close()
            connections = tuple(self._connections)
            for connection in connections:
                connection.cancel()
            await asyncio.gather(*connections, return_exceptions=True)

    def _accept(
        self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter
    ) -> None:
        connection = asyncio.create_task(self._forward(reader, writer))
        self._connections.add(connection)
        connection.add_done_callback(self._connections.discard)

    async def _forward(
        self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter
    ) -> None:
        tls_socket = writer.get_extra_info("ssl_object")
        self.handshakes.append(
            (tls_socket.version(), tls_socket.selected_alpn_protocol())
        )
        upstream_writer: asyncio.StreamWriter | None = None
        copy_tasks: list[asyncio.Task[None]] = []
        try:
            upstream_reader, upstream_writer = await asyncio.open_connection(
                self._upstream_host, self._upstream_port
            )
            copy_tasks = [
                asyncio.create_task(self._copy(reader, upstream_writer)),
                asyncio.create_task(self._copy(upstream_reader, writer)),
            ]
            await asyncio.wait(copy_tasks, return_when=asyncio.FIRST_COMPLETED)
        except (ConnectionError, ssl.SSLError):
            # Rejected handshakes and channel teardown reset test connections.
            pass
        finally:
            for copy_task in copy_tasks:
                copy_task.cancel()
            await asyncio.gather(*copy_tasks, return_exceptions=True)
            writer.close()
            if upstream_writer is not None:
                upstream_writer.close()
            try:
                await writer.wait_closed()
                if upstream_writer is not None:
                    await upstream_writer.wait_closed()
            except (ConnectionError, ssl.SSLError):
                # Rejected handshakes and channel teardown reset test connections.
                pass

    @staticmethod
    async def _copy(reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
        while payload := await reader.read(65536):
            writer.write(payload)
            await writer.drain()

    def _record_server_name(
        self,
        connection: ssl.SSLSocket | ssl.SSLObject,
        server_name: str | None,
        context: ssl.SSLSocket,
    ) -> None:
        self.server_names.append(server_name)
