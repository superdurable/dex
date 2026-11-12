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

"""Serves POST /orders and runs the Worker in the same process."""

from __future__ import annotations

import json
import os
import threading
from datetime import timedelta
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from dex import (
    Client,
    ClientOptions,
    FlowAlreadyStartedError,
    Registry,
    StartFlowOptions,
    StepExecutionId,
    WaitForStepCompletionOptions,
    Worker,
    WorkerOptions,
)

from quick_start.config import HTTP_PORT, SERVER_ADDRESS, new_shop_api, open_cache
from quick_start.order_flow import OrderFlow


def start_order(client: Client, order_flow: OrderFlow, order_id: str) -> None:
    """Starts the order's Flow once, however often the request is retried."""
    options = StartFlowOptions(
        request_id=f"start-{order_id}",
        ignore_already_started=True,
    )
    client.start_flow(order_flow, order_id, order_id, options)
    client.wait_for_step_completion(
        order_id,
        StepExecutionId("Charge"),
        WaitForStepCompletionOptions(request_timeout=timedelta(seconds=30)),
    )


def new_handler(client: Client, order_flow: OrderFlow) -> type[BaseHTTPRequestHandler]:
    class OrderHandler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:
            if self.path != "/orders":
                self.send_error(404)
                return
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            order_id = str(body["orderId"])
            try:
                start_order(client, order_flow, order_id)
            except FlowAlreadyStartedError:
                self._reply(409, {"error": f"order {order_id} was already started"})
                return
            self._reply(200, {"flowId": order_id, "charged": True})

        def _reply(self, status: int, payload: dict[str, object]) -> None:
            data = json.dumps(payload).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

    return OrderHandler


def main() -> None:
    order_flow = OrderFlow(new_shop_api())
    registry = Registry((order_flow,))
    worker_cache = open_cache(Path(".dex-blob-cache/worker"))
    client_cache = open_cache(Path(".dex-blob-cache/client"))
    worker = Worker(registry, worker_cache, WorkerOptions(server_address=SERVER_ADDRESS))
    client = Client(registry, client_cache, ClientOptions(server_address=SERVER_ADDRESS))
    worker_thread = threading.Thread(target=worker.start, daemon=True)
    server = ThreadingHTTPServer(("127.0.0.1", HTTP_PORT), new_handler(client, order_flow))
    try:
        worker_thread.start()
        worker.wait_pull_registered(timeout=30)
        print(f"Serving POST /orders on port {HTTP_PORT} (process {os.getpid()}).", flush=True)
        server.serve_forever()
    except KeyboardInterrupt:
        print("Stopping the service.", flush=True)
    finally:
        server.server_close()
        client.close()
        worker.close()
        client_cache.close()
        worker_cache.close()


if __name__ == "__main__":
    main()
