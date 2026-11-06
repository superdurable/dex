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
                "flow_service_tls.server_name must be a nonempty DNS name or IP address without a port"
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
            raise _unsupported_address()
        target = f"dns://{dns_target}"
    elif (
        re.search(r"[/?#]", server_address)
        or server_address.partition(":")[0].lower() in _RESOLVER_SCHEMES
    ):
        raise _unsupported_address()
    host, separator, port = endpoint.rpartition(":")
    if not separator:
        raise _unsupported_address()
    if host.startswith("[") and host.endswith("]"):
        host = host[1:-1]
        if not _is_ipv6(host):
            raise _unsupported_address()
    elif ":" in host:
        raise _unsupported_address()
    if "[" in host or "]" in host or not _is_valid_server_name(host):
        raise _unsupported_address()
    significant_port = port.lstrip("0")
    if (
        not re.fullmatch(r"[0-9]+", port)
        or len(significant_port) > 5
        or not 1 <= int(significant_port or "0") <= 65535
    ):
        raise ValueError("flow_service_tls requires server_address port 1-65535")
    return target


def _unsupported_address() -> ValueError:
    return ValueError(
        "flow_service_tls requires server_address as host:port, dns:///host:port, or dns://authority/host:port"
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
    try:
        bundled_roots = (
            files("grpc").joinpath("_cython", "_credentials", "roots.pem").read_bytes()
        )
        system_roots = ssl.create_default_context().get_ca_certs(binary_form=True)
        certificates = [bundled_roots] + [
            ssl.DER_cert_to_PEM_cert(certificate).encode("ascii")
            for certificate in system_roots
        ]
        certificates.extend(_system_root_directory_certificates())
        configured_file = os.environ.get("GRPC_DEFAULT_SSL_ROOTS_FILE_PATH")
        if configured_file:
            certificates.append(
                _additional_root_certificates(Path(configured_file).read_bytes())
            )
        return b"\n".join(certificates)
    except (OSError, ValueError, ssl.SSLError) as failure:
        raise ValueError("flow_service_tls: cannot load default root CAs") from failure


# OpenSSL loads hashed CA-directory entries lazily, so get_ca_certs alone omits their roots.
def _system_root_directory_certificates() -> list[bytes]:
    paths = ssl.get_default_verify_paths()
    root_directories = os.environ.get(paths.openssl_capath_env, paths.openssl_capath)
    certificates = []
    for directory_name in root_directories.split(os.pathsep):
        directory = Path(directory_name)
        if not directory_name or not directory.is_dir():
            continue
        for certificate_file in directory.iterdir():
            if (
                re.fullmatch(r"[0-9a-fA-F]{8}\.[0-9]+", certificate_file.name)
                and certificate_file.is_file()
            ):
                bundle = certificate_file.read_bytes()
                try:
                    certificates.append(_additional_root_certificates(bundle))
                except ValueError:
                    continue
    return certificates
