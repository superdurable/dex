# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import asyncio
import ssl
import socket
from dataclasses import fields
from importlib.resources import files
from pathlib import Path
from typing import cast
from unittest.mock import patch

import grpc
import pytest

from dex import (
    AsyncClient,
    AsyncWorker,
    BlobCache,
    Client,
    ClientOptions,
    FlowServiceTlsOptions,
    Registry,
    Worker,
    WorkerOptions,
)
from dex._flow_service_tls import (
    _additional_root_certificates,
    _default_root_certificates,
    flow_service_transport,
)
from integ.tls_transport import TlsCertificates, TlsProxy, create_certificates


@pytest.fixture
def certificates(tmp_path: Path) -> TlsCertificates:
    return create_certificates(tmp_path / "primary")


def test_tls_options_have_exactly_two_optional_fields() -> None:
    assert [field.name for field in fields(FlowServiceTlsOptions)] == [
        "additional_root_cas_pem",
        "server_name",
    ]
    assert FlowServiceTlsOptions() == FlowServiceTlsOptions(None, None)


@pytest.mark.parametrize(
    "address", ["unix:///tmp/dex.sock", "DNS:///localhost:443", "localhost:8801"]
)
def test_unset_tls_preserves_plaintext(address: str) -> None:
    transport = flow_service_transport(address, None)
    assert transport.target == address
    assert transport.credentials is None
    assert transport.channel_options == ()


@pytest.mark.parametrize(
    "address",
    [
        "localhost:443",
        "dex.example.com:443",
        "127.0.0.1:1",
        "[::1]:65535",
        "dns:///localhost:443",
        "DNS:///localhost:443",
        "dns://127.0.0.1:53/localhost:443",
    ],
)
def test_tls_accepts_host_port_and_dns_addresses(address: str) -> None:
    transport = flow_service_transport(address, FlowServiceTlsOptions())
    assert transport.target == address.replace("DNS://", "dns://")
    assert transport.credentials is not None
    assert transport.channel_options == ()


@pytest.mark.parametrize(
    "address",
    [
        "unix:8443",
        "Unix:8443",
        "passthrough:443",
        "dns:443",
        "unix:///tmp/dex.sock",
        "dns:///localhost/path:443",
        "https://localhost:443",
        "ipv4:127.0.0.1:443",
        "localhost",
        "::1:443",
        "[::1]",
        "local host:443",
        "dns:/localhost:443",
        "dns://bad authority/localhost:443",
        "user@localhost:443",
        "[localhost]:443",
        "[127.0.0.1]:443",
        "dns:///[localhost]:443",
        "localhost:443?query",
        "localhost:443#fragment",
        "[fe80::1%lo0]:443",
        "[[::1]]:443",
        ":443",
    ],
)
def test_tls_rejects_unsupported_addresses(address: str) -> None:
    with pytest.raises(ValueError, match="server_address as host:port") as failure:
        flow_service_transport(address, FlowServiceTlsOptions())
    assert f"got {address!r}" in str(failure.value)


@pytest.mark.parametrize(
    "port", ["0", "65536", "https", "", "+443", "-1", "４４３", "9" * 5000]
)
def test_tls_rejects_invalid_ports(port: str) -> None:
    address = f"localhost:{port}"
    with pytest.raises(ValueError, match="port 1-65535") as failure:
        flow_service_transport(address, FlowServiceTlsOptions())
    assert f"got {address!r}" in str(failure.value)


@pytest.mark.parametrize(
    "name",
    ["", "localhost:443", "bad name", "user@host", "host/path", "[::1]", "fe80::1%lo0"],
)
def test_tls_rejects_invalid_server_names(name: str) -> None:
    with pytest.raises(ValueError, match="server_name") as failure:
        flow_service_transport("localhost:443", FlowServiceTlsOptions(server_name=name))
    assert f"got {name!r}" in str(failure.value)


@pytest.mark.parametrize("name", ["localhost", "127.0.0.1", "::1"])
def test_server_name_sets_verification_and_authority(name: str) -> None:
    transport = flow_service_transport(
        "127.0.0.1:443", FlowServiceTlsOptions(server_name=name)
    )
    assert transport.channel_options == (
        ("grpc.ssl_target_name_override", name),
        ("grpc.default_authority", name),
    )


@pytest.mark.parametrize(
    "bundle",
    [
        b"invalid",
        b"-----BEGIN CERTIFICATE-----\ninvalid\n-----END CERTIFICATE-----",
        b"\xff",
    ],
)
def test_invalid_pem_is_rejected_without_echoing_certificates(bundle: bytes) -> None:
    with pytest.raises(
        ValueError, match="additional_root_cas_pem contains no PEM certificate"
    ) as failure:
        flow_service_transport("localhost:443", FlowServiceTlsOptions(bundle))
    assert repr(bundle) not in str(failure.value)


def test_pem_accepts_bytes_text_and_mixed_bundles(
    certificates: TlsCertificates,
) -> None:
    invalid = b"-----BEGIN CERTIFICATE-----\ninvalid\n-----END CERTIFICATE-----\n"
    assert (
        _additional_root_certificates(invalid + certificates.root_pem)
        == certificates.root_pem.strip()
    )
    assert (
        _additional_root_certificates(certificates.root_pem.decode())
        == certificates.root_pem.strip()
    )
    for bundle in [b"", "", None]:
        with patch(
            "grpc.ssl_channel_credentials", wraps=grpc.ssl_channel_credentials
        ) as credentials:
            flow_service_transport("localhost:443", FlowServiceTlsOptions(bundle))
            credentials.assert_called_once_with(None)


@pytest.mark.parametrize("has_configured_roots", [False, True])
def test_configured_default_roots_are_preserved(
    certificates: TlsCertificates,
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    has_configured_roots: bool,
) -> None:
    system = create_certificates(tmp_path / "system")
    extra = create_certificates(tmp_path / "extra")
    root_file = tmp_path / "default-roots.pem"
    root_file.write_bytes(certificates.root_pem)
    system_directory = tmp_path / "system-directory"
    system_directory.mkdir()
    (system_directory / "12345678.0").write_bytes(system.root_pem)
    monkeypatch.setenv("SSL_CERT_FILE", str(system.certificate_file.parent / "ca.pem"))
    monkeypatch.setenv("SSL_CERT_DIR", str(system_directory))
    if has_configured_roots:
        monkeypatch.setenv("GRPC_DEFAULT_SSL_ROOTS_FILE_PATH", str(root_file))
        expected_default = certificates.root_pem.strip()
    else:
        monkeypatch.delenv("GRPC_DEFAULT_SSL_ROOTS_FILE_PATH", raising=False)
        expected_default = (
            files("grpc").joinpath("_cython", "_credentials", "roots.pem").read_bytes()
        )
    with patch(
        "grpc.ssl_channel_credentials", wraps=grpc.ssl_channel_credentials
    ) as credentials:
        flow_service_transport("localhost:443", FlowServiceTlsOptions(extra.root_pem))
    bundle = cast(bytes, credentials.call_args.args[0])
    assert bundle == expected_default + b"\n" + extra.root_pem.strip()
    assert system.root_pem.strip() not in bundle
    if has_configured_roots:
        bundled_roots = (
            files("grpc").joinpath("_cython", "_credentials", "roots.pem").read_bytes()
        )
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
        context.load_verify_locations(cadata=bundled_roots.decode("ascii"))
        for certificate in context.get_ca_certs(binary_form=True):
            assert ssl.DER_cert_to_PEM_cert(certificate).strip().encode() not in bundle
    else:
        assert certificates.root_pem.strip() not in bundle


def test_unreadable_default_roots_fail_construction(
    certificates: TlsCertificates, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    monkeypatch.setenv("GRPC_DEFAULT_SSL_ROOTS_FILE_PATH", str(tmp_path / "missing"))
    with pytest.raises(ValueError, match="cannot load default root CAs"):
        flow_service_transport(
            "localhost:443", FlowServiceTlsOptions(certificates.root_pem)
        )
    monkeypatch.delenv("GRPC_DEFAULT_SSL_ROOTS_FILE_PATH")
    with patch("dex._flow_service_tls.files", side_effect=OSError("unreadable bundle")):
        with pytest.raises(ValueError, match="cannot load default root CAs"):
            flow_service_transport(
                "localhost:443", FlowServiceTlsOptions(certificates.root_pem)
            )


def test_configured_roots_do_not_read_bundled_roots(
    certificates: TlsCertificates, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    root_file = tmp_path / "default-roots.pem"
    root_file.write_bytes(certificates.root_pem)
    monkeypatch.setenv("GRPC_DEFAULT_SSL_ROOTS_FILE_PATH", str(root_file))
    with patch("dex._flow_service_tls.files", side_effect=OSError("no bundled roots")):
        assert _default_root_certificates() == certificates.root_pem.strip()


def test_worker_loads_default_roots_once(certificates: TlsCertificates) -> None:
    with patch(
        "dex._flow_service_tls._default_root_certificates",
        side_effect=[certificates.root_pem, ValueError("second read")],
    ) as read_roots:
        with Worker(
            Registry([]),
            cast(BlobCache, object()),
            WorkerOptions(
                flow_service_tls=FlowServiceTlsOptions(certificates.root_pem)
            ),
        ):
            read_roots.assert_called_once_with()


def test_async_worker_loads_default_roots_once(certificates: TlsCertificates) -> None:
    asyncio.run(_check_async_worker_loads_default_roots_once(certificates))


async def _check_async_worker_loads_default_roots_once(
    certificates: TlsCertificates,
) -> None:
    with patch(
        "dex._flow_service_tls._default_root_certificates",
        side_effect=[certificates.root_pem, ValueError("second read")],
    ) as read_roots:
        async with AsyncWorker(
            Registry([]),
            cast(BlobCache, object()),
            WorkerOptions(
                flow_service_tls=FlowServiceTlsOptions(certificates.root_pem)
            ),
        ):
            read_roots.assert_called_once_with()


def test_sync_constructors_validate_tls() -> None:
    cache = cast(BlobCache, object())
    for constructor, options in [(Client, ClientOptions), (Worker, WorkerOptions)]:
        with pytest.raises(ValueError, match="server_name"):
            constructor(
                Registry([]),
                cache,
                options(flow_service_tls=FlowServiceTlsOptions(server_name="")),
            )


def test_async_constructors_validate_tls() -> None:
    asyncio.run(_check_async_constructors())


async def _check_async_constructors() -> None:
    cache = cast(BlobCache, object())
    for constructor, options in [
        (AsyncClient, ClientOptions),
        (AsyncWorker, WorkerOptions),
    ]:
        with pytest.raises(ValueError, match="server_name"):
            constructor(
                Registry([]),
                cache,
                options(flow_service_tls=FlowServiceTlsOptions(server_name="")),
            )


@pytest.mark.parametrize("protocols", [("http/1.1",), ()])
def test_grpc_rejects_server_without_h2(
    certificates: TlsCertificates, protocols: tuple[str, ...]
) -> None:
    with TlsProxy(certificates, "127.0.0.1:1", protocols) as proxy:
        transport = flow_service_transport(
            proxy.address, FlowServiceTlsOptions(certificates.root_pem)
        )
        assert transport.credentials is not None
        with grpc.secure_channel(transport.target, transport.credentials) as channel:
            with pytest.raises(grpc.RpcError) as failure:
                channel.unary_unary(
                    "/test/transport",
                    request_serializer=bytes,
                    response_deserializer=bytes,
                )(b"", timeout=2)
        assert failure.value.code() is grpc.StatusCode.UNAVAILABLE
        assert "ALPN" in (failure.value.details() or "")


def test_grpc_rejects_tls_older_than_12(certificates: TlsCertificates) -> None:
    with pytest.warns(DeprecationWarning):
        proxy = TlsProxy(
            certificates,
            "127.0.0.1:1",
            maximum_version=ssl.TLSVersion.TLSv1_1,
            minimum_version=ssl.TLSVersion.TLSv1_1,
        )
    with proxy:
        transport = flow_service_transport(
            proxy.address, FlowServiceTlsOptions(certificates.root_pem)
        )
        assert transport.credentials is not None
        with grpc.secure_channel(transport.target, transport.credentials) as channel:
            with pytest.raises(grpc.RpcError) as failure:
                channel.unary_unary(
                    "/test/transport",
                    request_serializer=bytes,
                    response_deserializer=bytes,
                )(b"", timeout=2)
        assert failure.value.code() is grpc.StatusCode.UNAVAILABLE
        assert not proxy.handshakes
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
        context.load_verify_locations(cadata=certificates.root_pem.decode())
        with pytest.warns(DeprecationWarning):
            context.minimum_version = ssl.TLSVersion.TLSv1_1
            context.maximum_version = ssl.TLSVersion.TLSv1_1
        context.set_ciphers("DEFAULT:@SECLEVEL=0")
        with socket.create_connection(
            ("127.0.0.1", proxy.port), timeout=2
        ) as connection:
            with context.wrap_socket(
                connection, server_hostname="localhost"
            ) as tls_socket:
                assert tls_socket.version() == "TLSv1.1"
