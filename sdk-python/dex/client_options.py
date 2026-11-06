# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Sustainable Use License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from dataclasses import dataclass

from dex.credential import Credential
from dex.flow_service_tls_options import FlowServiceTlsOptions


@dataclass(frozen=True)
class ClientOptions:
    """Configure FlowService connectivity and bearer credentials.

    Attributes:
        server_address: The Dex gRPC target. Defaults to ``"localhost:8801"``
            and must not include an HTTP scheme. Plaintext unless TLS is set.
        flow_service_tls: TLS for all outbound FlowService connections.
            Defaults to ``None`` for plaintext. See ``FlowServiceTlsOptions``
            for trust, address validation, and a complete example.
        credential: Supplies the current bearer token for every FlowService call.
            ``None`` sends no authorization metadata. A token error fails the
            call before contacting Dex. AsyncClient accepts an awaitable token
            and bounds its lookup by the RPC timeout when set. Synchronous token
            lookup must return promptly. Enable TLS to protect bearer tokens in transit.
    """

    server_address: str = "localhost:8801"
    credential: Credential | None = None
    flow_service_tls: FlowServiceTlsOptions | None = None
