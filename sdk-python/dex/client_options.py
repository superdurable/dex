# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from dataclasses import dataclass

from dex.object_encoder import ObjectEncoder

@dataclass
class ClientOptions:
    server_url: str
    worker_url: str
    object_encoder: ObjectEncoder
    api_timeout: int = 60
    long_poll_api_max_wait_time_seconds: int = 10

    @classmethod
    def local_default(cls):
        return ClientOptions(
            server_url="http://localhost:8801",
            worker_url="http://localhost:8802",
            object_encoder=ObjectEncoder.default,
        )
