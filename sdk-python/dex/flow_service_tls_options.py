# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from dataclasses import dataclass


@dataclass(frozen=True)
class FlowServiceTlsOptions:
    """Enable verified TLS for a Client or Worker's outbound connections.

    Set ``ClientOptions.flow_service_tls`` and ``WorkerOptions.flow_service_tls``
    independently. ``None`` keeps plaintext gRPC. An instance, including one
    with no arguments, enables TLS 1.2 or newer with HTTP/2 ALPN negotiation.
    Applies to synchronous and asynchronous Clients and pull Workers, including
    startup, blob hydration, and task streams. Configure credential token
    endpoints separately. Bearer credentials remain call metadata.

    TLS addresses accept ``host:port``, ``dns:///host:port``, and
    ``dns://authority/host:port`` with a port from 1 through 65535. The resolver
    authority does not determine the certificate name. Connections open lazily;
    certificate and handshake errors surface on a Client call or Worker start.
    The endpoint must negotiate ALPN ``h2`` and forward gRPC over HTTP/2.

    Attributes:
        additional_root_cas_pem: PEM-encoded root certificates as bytes or text.
            Defaults to ``None``; empty bytes or text also use gRPC's default
            trust store. Nonempty bundles add roots to grpcio's bundled roots,
            Python's system roots, and ``GRPC_DEFAULT_SSL_ROOTS_FILE_PATH`` when
            set. Defaults are never replaced by the added roots. At least one
            certificate must parse; other malformed or non-certificate blocks
            are skipped. Files and certificates are read at Client or Worker
            construction and are not watched for changes.
        server_name: DNS name or IP address overriding certificate verification,
            SNI, and HTTP/2 authority. Defaults to ``None``, which verifies the
            address host and preserves its host and port as authority. An
            explicit name is sent as authority without a port. IP names require
            an IP SAN and send no SNI. Prefer a DNS name for IPv6 overrides,
            because strict HTTP/2 gateways reject unbracketed IPv6 authority.
            Empty names, ports, paths, and scoped IPv6 names are invalid.

    Raises:
        ValueError: During Client or Worker construction if the TLS address,
            server name, or nonempty PEM bundle is invalid, or default roots
            cannot be loaded when adding certificates.

    Examples:
        >>> from concurrent.futures import ThreadPoolExecutor
        >>> from pathlib import Path
        >>> from dex import Client, ClientOptions, FlowServiceTlsOptions
        >>> from dex import Worker, WorkerOptions
        >>> tls = FlowServiceTlsOptions(
        ...     additional_root_cas_pem=Path("/etc/dex/root-ca.pem").read_bytes(),
        ...     server_name="dex.example.com",
        ... )
        >>> address = "dex.example.com:443"
        >>> with Worker(registry, cache, WorkerOptions(
        ...     server_address=address, flow_service_tls=tls,
        ... )) as worker, Client(registry, cache, ClientOptions(
        ...     server_address=address, flow_service_tls=tls,
        ... )) as client, ThreadPoolExecutor(max_workers=1) as executor:
        ...     started = executor.submit(worker.start)
        ...     try:
        ...         worker.wait_pull_registered(timeout=30)
        ...         client.start_flow(flow, flow_id, input_value)
        ...     finally:
        ...         worker.stop()
        ...         started.result(timeout=10)
    """

    additional_root_cas_pem: bytes | str | None = None
    server_name: str | None = None
