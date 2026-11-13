/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

/** Returns the host's launch command with the prompt quoted on one line, preceded by its Dex Skills install lines when `withInstall` is set. */
export function codingAgentCommand(host: {install: string; launch: string}, prompt: string, withInstall = false): string {
  // The prompt goes inside double quotes in bash and zsh, where these characters would be interpreted.
  if (/["$`!\\]/.test(prompt)) throw new Error(`CodingAgentBox prompt has a character the shell would interpret: ${prompt}`);
  const launch = `${host.launch} "${prompt.trim()}"`;
  return withInstall ? `${host.install}\n${launch}` : launch;
}
