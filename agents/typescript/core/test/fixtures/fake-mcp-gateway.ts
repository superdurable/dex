// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// A gateway in front of an MCP server that breaks tools/call answers after the server ran the call:
// `node fake-mcp-gateway.js <port> <upstream url> <mode>`. Mode `502` answers with a text/plain 502;
// mode `internal-error` with a JSON-RPC InternalError (-32603). Other requests pass through.

import { createServer, type IncomingMessage } from "node:http";

import { fakeMcpReadyLine } from "./fake-mcp-server-names.js";

const hopByHopHeaders = new Set(["host", "content-length", "transfer-encoding", "connection", "content-encoding"]);

async function readBody(request: IncomingMessage): Promise<Buffer> {
  const chunks: Buffer[] = [];
  for await (const chunk of request) {
    chunks.push(chunk as Buffer);
  }
  return Buffer.concat(chunks);
}

function requestMethodAndId(body: Buffer): { readonly method: unknown; readonly id: unknown } {
  try {
    const message = JSON.parse(body.toString("utf8")) as { method?: unknown; id?: unknown };
    return { method: message.method, id: message.id };
  } catch {
    // Not JSON-RPC; forward it unchanged.
    return { method: undefined, id: undefined };
  }
}

const [port, upstreamUrl, mode] = process.argv.slice(2);
if (port === undefined || upstreamUrl === undefined || (mode !== "502" && mode !== "internal-error")) {
  throw new Error("usage: fake-mcp-gateway.js <port> <upstream url> 502|internal-error");
}
const gateway = createServer((request, response) => {
  void (async () => {
    const body = await readBody(request);
    const headers = new Headers();
    for (const [name, value] of Object.entries(request.headers)) {
      if (!hopByHopHeaders.has(name) && typeof value === "string") {
        headers.set(name, value);
      }
    }
    const upstream = await fetch(upstreamUrl, {
      method: request.method ?? "POST",
      headers,
      ...(body.length === 0 ? {} : { body }),
    });
    const upstreamBody = Buffer.from(await upstream.arrayBuffer());
    const { method, id } = requestMethodAndId(body);
    if (method === "tools/call" && mode === "502") {
      response.writeHead(502, { "content-type": "text/plain" }).end("Bad Gateway");
      return;
    }
    if (method === "tools/call") {
      response
        .writeHead(200, { "content-type": "application/json" })
        .end(JSON.stringify({ jsonrpc: "2.0", id, error: { code: -32603, message: "upstream failed" } }));
      return;
    }
    const responseHeaders: Record<string, string> = {};
    upstream.headers.forEach((value, name) => {
      if (!hopByHopHeaders.has(name)) {
        responseHeaders[name] = value;
      }
    });
    response.writeHead(upstream.status, responseHeaders).end(upstreamBody);
  })().catch((error: unknown) => {
    console.error("fake MCP gateway request failed", error);
    if (!response.headersSent) {
      response.writeHead(500).end();
    }
  });
});
gateway.listen(Number(port), "127.0.0.1", () => {
  console.log(`${fakeMcpReadyLine} on ${port}`);
});
