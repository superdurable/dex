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
            trust store. Nonempty bundles use the gRPC default roots (grpcio's
            bundled roots, or ``GRPC_DEFAULT_SSL_ROOTS_FILE_PATH`` when set)
            plus the added roots. A configured root file takes precedence over
            system roots. Adding roots fails when
            ``GRPC_USE_SYSTEM_ROOTS_OVER_LANGUAGE_CALLBACK`` is true and
            ``GRPC_NOT_USE_SYSTEM_SSL_ROOTS`` is not true, unless a root file
            is configured. True values are ``true``, ``t``, ``yes``, ``y``, and
            ``1``, ignoring case. This includes system roots selected with
            ``GRPC_SYSTEM_SSL_ROOTS_DIR``. To add roots in that configuration,
            disable the system-roots preference and pass the full PEM bundle
            here, or set ``GRPC_DEFAULT_SSL_ROOTS_FILE_PATH`` to the full bundle.
            Pass an OS or corporate CA bundle as ``additional_root_cas_pem``
            to trust those certificates. ``SSL_CERT_FILE`` and ``SSL_CERT_DIR``
            do not affect this trust. At least one
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
            cannot be loaded when adding certificates, or added roots cannot
            be combined with the configured gRPC system roots.

    Examples:
        Construct Client options for a TLS endpoint. The CA file must already
        exist. Pass these options to a Client with a Registry and BlobCache.

        >>> from pathlib import Path
        >>> from dex import ClientOptions, FlowServiceTlsOptions
        >>> tls = FlowServiceTlsOptions(
        ...     additional_root_cas_pem=Path("/etc/dex/root-ca.pem").read_bytes(),
        ...     server_name="dex.example.com",
        ... )
        >>> options = ClientOptions(
        ...     server_address="dex.example.com:443", flow_service_tls=tls,
        ... )
    """

    additional_root_cas_pem: bytes | str | None = None
    server_name: str | None = None
