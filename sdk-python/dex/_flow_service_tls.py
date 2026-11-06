# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import ipaddress
import os
import re
import ssl
from dataclasses import dataclass
from importlib.resources import files
from pathlib import Path

import grpc

from dex.flow_service_tls_options import FlowServiceTlsOptions

_RESOLVER_SCHEMES = {"dns", "ipv4", "ipv6", "passthrough", "unix", "unix-abstract"}
_PEM_CERTIFICATE = re.compile(
    rb"-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----", re.DOTALL
)
_TRUE_ENVIRONMENT_VALUES = {"true", "t", "yes", "y", "1"}


@dataclass(frozen=True)
class FlowServiceTransport:
    target: str
    credentials: grpc.ChannelCredentials | None
    channel_options: tuple[tuple[str, str], ...] = ()


def flow_service_transport(
    server_address: str, options: FlowServiceTlsOptions | None
) -> FlowServiceTransport:
    if options is None:
        return FlowServiceTransport(server_address, None)
    target = _flow_service_tls_target(server_address)
    channel_options: tuple[tuple[str, str], ...] = ()
    if options.server_name is not None:
        if not _is_valid_server_name(options.server_name):
            raise ValueError(
                "flow_service_tls.server_name must be a nonempty DNS name or IP address without a port; "
                f"got {options.server_name!r}"
            )
        channel_options = (
            ("grpc.ssl_target_name_override", options.server_name),
            ("grpc.default_authority", options.server_name),
        )
    root_certificates = None
    if options.additional_root_cas_pem:
        certificates = _additional_root_certificates(options.additional_root_cas_pem)
        root_certificates = _default_root_certificates() + b"\n" + certificates
    return FlowServiceTransport(
        target, grpc.ssl_channel_credentials(root_certificates), channel_options
    )


def _flow_service_tls_target(server_address: str) -> str:
    target = endpoint = server_address
    scheme, separator, dns_target = server_address.partition("://")
    if separator and scheme.lower() == "dns":
        authority, separator, endpoint = dns_target.partition("/")
        if (
            not separator
            or re.search(r"[ \t\r\n?#@]", authority)
            or re.search(r"[/?#]", endpoint)
        ):
            raise _unsupported_address(server_address)
        target = f"dns://{dns_target}"
    elif (
        re.search(r"[/?#]", server_address)
        or server_address.partition(":")[0].lower() in _RESOLVER_SCHEMES
    ):
        raise _unsupported_address(server_address)
    host, separator, port = endpoint.rpartition(":")
    if not separator:
        raise _unsupported_address(server_address)
    if host.startswith("[") and host.endswith("]"):
        host = host[1:-1]
        if not _is_ipv6(host):
            raise _unsupported_address(server_address)
    elif ":" in host:
        raise _unsupported_address(server_address)
    if "[" in host or "]" in host or not _is_valid_server_name(host):
        raise _unsupported_address(server_address)
    significant_port = port.lstrip("0")
    if (
        not re.fullmatch(r"[0-9]+", port)
        or len(significant_port) > 5
        or not 1 <= int(significant_port or "0") <= 65535
    ):
        raise ValueError(
            f"flow_service_tls requires server_address port 1-65535; got {server_address!r}"
        )
    return target


def _unsupported_address(server_address: str) -> ValueError:
    return ValueError(
        "flow_service_tls requires server_address as host:port, dns:///host:port, or dns://authority/host:port; "
        f"got {server_address!r}"
    )


def _is_valid_server_name(name: str) -> bool:
    return (
        bool(name)
        and not re.search(r"[\s/#?@]", name)
        and (":" not in name or _is_ipv6(name))
    )


def _is_ipv6(name: str) -> bool:
    try:
        return (
            isinstance(ipaddress.ip_address(name), ipaddress.IPv6Address)
            and "%" not in name
        )
    except ValueError:
        return False


def _additional_root_certificates(bundle: bytes | str) -> bytes:
    encoded_bundle = bundle.encode("utf-8") if isinstance(bundle, str) else bundle
    certificates = []
    for certificate in _PEM_CERTIFICATE.findall(encoded_bundle):
        try:
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
            context.load_verify_locations(cadata=certificate.decode("ascii"))
        except (ValueError, ssl.SSLError, UnicodeError):
            continue
        certificates.append(certificate)
    if not certificates:
        raise ValueError(
            "flow_service_tls.additional_root_cas_pem contains no PEM certificate"
        )
    return b"\n".join(certificates)


def _default_root_certificates() -> bytes:
    configured_file = os.environ.get("GRPC_DEFAULT_SSL_ROOTS_FILE_PATH")
    if (
        not configured_file
        and _is_environment_flag_true("GRPC_USE_SYSTEM_ROOTS_OVER_LANGUAGE_CALLBACK")
        and not _is_environment_flag_true("GRPC_NOT_USE_SYSTEM_SSL_ROOTS")
    ):
        raise ValueError(
            "flow_service_tls: added roots cannot be combined with gRPC system roots; "
            "disable GRPC_USE_SYSTEM_ROOTS_OVER_LANGUAGE_CALLBACK and pass the full "
            "bundle via additional_root_cas_pem, or set GRPC_DEFAULT_SSL_ROOTS_FILE_PATH "
            "to the full bundle"
        )
    try:
        if configured_file:
            return _additional_root_certificates(Path(configured_file).read_bytes())
        return (
            files("grpc").joinpath("_cython", "_credentials", "roots.pem").read_bytes()
        )
    except (OSError, ValueError, ssl.SSLError) as failure:
        source = f" from {configured_file!r}" if configured_file else ""
        raise ValueError(
            f"flow_service_tls: cannot load default root CAs{source}"
        ) from failure


def _is_environment_flag_true(name: str) -> bool:
    return os.environ.get(name, "").lower() in _TRUE_ENVIRONMENT_VALUES
