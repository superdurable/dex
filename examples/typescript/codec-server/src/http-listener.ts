/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import { createServer, type RequestListener, type Server } from "node:http";
import type { AddressInfo } from "node:net";

import type { CodecServerLogger } from "./codec-server.ts";

/** A listening HTTP server and its graceful shutdown. */
export interface CodecHttpListener {
  readonly server: Server;
  readonly address: AddressInfo;
  /**
   * Stops accepting connections and waits for in-flight requests.
   * @returns true when every request finished within the shutdown timeout; false when the
   * remaining connections were closed forcibly.
   */
  shutDownGracefully(): Promise<boolean>;
}

/**
 * Starts the plain HTTP server with request, header, and keep-alive timeouts.
 * @returns The listener once it accepts connections.
 */
export async function listenCodecHttpServer(
  handler: RequestListener,
  host: string | undefined,
  port: number,
  shutdownTimeoutMs: number,
  logger: CodecServerLogger,
): Promise<CodecHttpListener> {
  const server = createServer({
    requestTimeout: 60_000,
    headersTimeout: 10_000,
    keepAliveTimeout: 5_000,
    maxHeaderSize: 64 * 1024,
  }, handler);
  server.on("clientError", (_error, socket) => {
    if (socket.writable) {
      socket.end("HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n");
    } else {
      socket.destroy();
    }
  });
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(port, host, () => {
      server.off("error", reject);
      resolve();
    });
  });
  const address = server.address() as AddressInfo;
  return {
    server,
    address,
    shutDownGracefully: () => new Promise<boolean>((resolve) => {
      logger("info", "codec server shutting down", { timeout_ms: shutdownTimeoutMs });
      // Keep-alive connections become idle after their in-flight request; close them as they do.
      const idleSweep = setInterval(() => server.closeIdleConnections(), 100);
      const forceTimer = setTimeout(() => {
        clearInterval(idleSweep);
        server.closeAllConnections();
        resolve(false);
      }, shutdownTimeoutMs);
      server.close(() => {
        clearInterval(idleSweep);
        clearTimeout(forceTimer);
        resolve(true);
      });
      server.closeIdleConnections();
    }),
  };
}
