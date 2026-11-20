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

// A deployable codec server for Dex Web and dexcli. It is not a Worker and never connects to Dex.

import { createPayloadCodecHttpServer, type CodecServerLogger } from "./codec-server.ts";
import { loadCodecServerConfig } from "./config.ts";
import { listenCodecHttpServer } from "./http-listener.ts";

const writeJsonLogLine: CodecServerLogger = (level, message, fields) => {
  process.stdout.write(`${JSON.stringify({ time: new Date().toISOString(), level, message, ...fields })}\n`);
};

async function runCodecServer(): Promise<void> {
  const config = loadCodecServerConfig(process.argv.slice(2), process.env);
  const codecServer = await createPayloadCodecHttpServer(config, writeJsonLogLine);
  const listener = await listenCodecHttpServer(
    codecServer.handleRequest,
    config.listenHost,
    config.listenPort,
    config.shutdownTimeoutMs,
    writeJsonLogLine,
  );
  writeJsonLogLine("info", "codec server listening", {
    address: `${listener.address.address}:${listener.address.port}`,
    auth_mode: config.authMode,
    allowed_origins: config.allowedOrigins,
  });
  const shutDown = (): void => {
    process.off("SIGTERM", shutDown);
    process.off("SIGINT", shutDown);
    void listener.shutDownGracefully().then((finished) => {
      if (!finished) {
        writeJsonLogLine("error", "graceful shutdown timed out; closed remaining connections", {});
        process.exitCode = 1;
      }
    });
  };
  process.on("SIGTERM", shutDown);
  process.on("SIGINT", shutDown);
}

runCodecServer().catch((failure: unknown) => {
  // Configuration and key file errors never contain key bytes or tokens.
  const message = failure instanceof Error ? failure.message : "unknown failure";
  writeJsonLogLine("error", "codec server stopped", { error: message });
  process.exitCode = 1;
});
