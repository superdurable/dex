# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Assembles the codec server, serves it, and shuts it down gracefully on SIGTERM."""

from __future__ import annotations

import asyncio
import logging
import os
import signal
import sys
from collections.abc import Mapping, Sequence

from aiohttp import web

from dex_codec_server.auth import SingleTenantPayloadCodecSelector, create_request_authenticator
from dex_codec_server.codec_server import PayloadCodecHttpServer
from dex_codec_server.config import (
    CodecServerConfig,
    CodecServerConfigurationError,
    load_codec_server_config,
)
from dex_codec_server.cors import CorsOriginAllowList
from dex_codec_server.json_logging import LOG_FIELDS_ATTRIBUTE, configure_json_logging
from dex_codec_server.keys import (
    create_aes_gcm_payload_codec,
    create_payload_encryption_key_provider,
)

# Request header lines and fields above this size get 400.
MAX_REQUEST_HEADER_FIELD_BYTES = 16 * 1024


def create_payload_codec_http_server(
    config: CodecServerConfig, logger: logging.Logger
) -> PayloadCodecHttpServer:
    """Build the server and its collaborators from the configuration.

    Raises:
        OSError: When the key file cannot be read.
        ValueError: When the keys are invalid. The message holds no key bytes.
    """
    key_provider = create_payload_encryption_key_provider(config)
    codec = create_aes_gcm_payload_codec(
        key_provider, config.payload_size_limits.max_plaintext_bytes
    )
    return PayloadCodecHttpServer(
        authenticator=create_request_authenticator(config),
        # Multi-tenant: select a codec per authenticated caller here. See README.md.
        codec_selector=SingleTenantPayloadCodecSelector(codec),
        cors_origins=CorsOriginAllowList(config.allowed_origins, config.allow_credentials),
        payload_size_limits=config.payload_size_limits,
        max_request_body_bytes=config.max_request_body_bytes,
        max_items_per_request=config.max_items_per_request,
        max_decoded_bytes_per_request=config.max_decoded_bytes_per_request,
        logger=logger,
    )


async def start_codec_server(
    server: PayloadCodecHttpServer, config: CodecServerConfig, logger: logging.Logger
) -> web.AppRunner:
    """Start listening and return the runner that ``stop_codec_server`` stops."""
    runner = web.AppRunner(
        server.create_application(),
        handle_signals=False,
        access_log=None,
        shutdown_timeout=config.shutdown_timeout_seconds,
        max_field_size=MAX_REQUEST_HEADER_FIELD_BYTES,
        max_line_size=MAX_REQUEST_HEADER_FIELD_BYTES,
    )
    await runner.setup()
    site = web.TCPSite(runner, config.listen_host, config.listen_port)
    await site.start()
    logger.info(
        "codec server listening",
        extra={
            LOG_FIELDS_ATTRIBUTE: {
                "addresses": [f"{address[0]}:{address[1]}" for address in runner.addresses],
                "auth_mode": config.auth_mode,
                "allowed_origins": list(config.allowed_origins),
            }
        },
    )
    return runner


async def stop_codec_server(
    runner: web.AppRunner, config: CodecServerConfig, logger: logging.Logger
) -> None:
    """Stop accepting connections and let in-flight requests finish within the shutdown timeout."""
    logger.info(
        "codec server shutting down",
        extra={LOG_FIELDS_ATTRIBUTE: {"timeout_seconds": config.shutdown_timeout_seconds}},
    )
    await runner.cleanup()


async def serve_until_signalled(config: CodecServerConfig, logger: logging.Logger) -> None:
    """Serve until SIGTERM or SIGINT, then shut down gracefully."""
    server = create_payload_codec_http_server(config, logger)
    stop_requested = asyncio.Event()
    loop = asyncio.get_running_loop()
    for stop_signal in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(stop_signal, stop_requested.set)
    runner = await start_codec_server(server, config, logger)
    try:
        await stop_requested.wait()
    finally:
        await stop_codec_server(runner, config, logger)


def run_codec_server(arguments: Sequence[str], environment: Mapping[str, str]) -> int:
    """Load the configuration and serve.

    Returns:
        The process exit code: 0 after a graceful shutdown, 1 on a startup error.
    """
    configure_json_logging()
    logger = logging.getLogger("dex_codec_server")
    try:
        config = load_codec_server_config(arguments, environment)
        asyncio.run(serve_until_signalled(config, logger))
    except (CodecServerConfigurationError, ValueError, OSError) as error:
        # These messages name settings, paths, and key IDs, never key bytes or tokens.
        logger.error(
            "codec server stopped",
            extra={LOG_FIELDS_ATTRIBUTE: {"error": str(error)}},
        )
        return 1
    return 0


def main() -> None:
    """Run the codec server process."""
    sys.exit(run_codec_server(sys.argv[1:], os.environ))
