# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A reachable gRPC server that answers every method with UNAVAILABLE; prints its port."""

from __future__ import annotations

import asyncio
from typing import Any

import grpc

#: The details every call fails with.
UNAVAILABLE_DETAILS = "the server is warming up"


class _UnavailableEverywhere(grpc.GenericRpcHandler):  # type: ignore[misc]
    def service(self, handler_call_details: Any) -> Any:
        async def refuse(request: bytes, context: Any) -> bytes:
            await context.abort(grpc.StatusCode.UNAVAILABLE, UNAVAILABLE_DETAILS)
            return b""

        return grpc.unary_unary_rpc_method_handler(refuse)


async def serve() -> None:
    """Serve until killed."""
    server = grpc.aio.server()
    server.add_generic_rpc_handlers((_UnavailableEverywhere(),))
    port = server.add_insecure_port("127.0.0.1:0")
    await server.start()
    print(port, flush=True)
    await server.wait_for_termination()


if __name__ == "__main__":
    asyncio.run(serve())
