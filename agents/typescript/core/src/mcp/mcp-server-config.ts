// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * Launches one MCP server as a child process and talks to it over stdio.
 *
 * Build it from Worker configuration, never from Flow input, a user message, or a browser: the command
 * runs with the Worker's privileges. Every request starts a new process, so a server that keeps state
 * between calls must keep it outside the process.
 *
 * @example
 * ```ts
 * const files: McpStdioServerConfig = {
 *   transport: "stdio",
 *   name: "files",
 *   command: "npx",
 *   args: ["-y", "@modelcontextprotocol/server-filesystem", "/srv/reports"],
 * };
 * ```
 */
export interface McpStdioServerConfig {
  /** Discriminator. */
  readonly transport: "stdio";
  /**
   * Server name, unique within one {@link McpToolSource}. It prefixes the agent tool names, so it may
   * contain only letters, digits, `_`, and `-`.
   */
  readonly name: string;
  /** Executable to run. */
  readonly command: string;
  /** Command-line arguments. Defaults to none. */
  readonly args?: readonly string[];
  /**
   * Environment variables merged over the MCP SDK's default environment, which keeps only variables
   * such as `PATH` and `HOME`. Defaults to none.
   */
  readonly env?: Readonly<Record<string, string>>;
  /** Working directory. Defaults to the Worker's. */
  readonly cwd?: string;
  /**
   * Whether the server's `readOnlyHint`, `idempotentHint`, and `destructiveHint` annotations pick a
   * tool's effect. Defaults to `true`; set `false` for a server whose hints you do not trust, so its
   * tools keep the session default unless a {@link ToolPolicy} names them.
   */
  readonly trustsToolAnnotations?: boolean;
}

/**
 * Connects to one MCP server over the Streamable HTTP transport.
 *
 * Build it from Worker configuration, never from Flow input, a user message, or a browser. Read
 * credentials such as bearer tokens from the Worker's environment or secret store and pass them in
 * {@link headers}. Every request opens a new connection and protocol session, so a server restart
 * between two calls is harmless.
 *
 * @example
 * ```ts
 * const tickets: McpStreamableHttpServerConfig = {
 *   transport: "streamable_http",
 *   name: "tickets",
 *   url: "https://tickets.example.com/mcp",
 *   headers: { Authorization: `Bearer ${process.env.TICKETS_TOKEN}` },
 * };
 * ```
 */
export interface McpStreamableHttpServerConfig {
  /** Discriminator. */
  readonly transport: "streamable_http";
  /**
   * Server name, unique within one {@link McpToolSource}. It prefixes the agent tool names, so it may
   * contain only letters, digits, `_`, and `-`.
   */
  readonly name: string;
  /** The server's MCP endpoint, an `http` or `https` URL. */
  readonly url: string;
  /** HTTP headers sent with every request, such as `Authorization`. Defaults to none. */
  readonly headers?: Readonly<Record<string, string>>;
  /** Whether the server's annotations pick a tool's effect. Defaults to `true`. */
  readonly trustsToolAnnotations?: boolean;
}

/** One MCP server configuration: stdio or Streamable HTTP. */
export type McpServerConfig = McpStdioServerConfig | McpStreamableHttpServerConfig;

const serverNamePattern = /^[A-Za-z0-9_-]+$/;

/** Throws a `TypeError` for an invalid configuration. */
export function validateMcpServerConfig(server: McpServerConfig): void {
  if (!serverNamePattern.test(server.name)) {
    throw new TypeError(`MCP server name ${JSON.stringify(server.name)} must be non-empty letters, digits, '_', or '-'`);
  }
  if (server.transport === "stdio") {
    if (server.command === "") {
      throw new TypeError(`MCP server ${server.name} needs a command`);
    }
    return;
  }
  if (server.transport !== "streamable_http") {
    throw new TypeError(`MCP server ${(server as { readonly name: string }).name} has an unknown transport`);
  }
  const protocol = URL.canParse(server.url) ? new URL(server.url).protocol : "";
  if (protocol !== "http:" && protocol !== "https:") {
    throw new TypeError(`MCP server ${server.name} needs an http or https URL`);
  }
}
