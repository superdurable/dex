/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

/** Returns the host's Dex Skills install lines, then its launch command with the prompt quoted on one line. */
export function codingAgentCommand(host: {install: string; launch: string}, prompt: string): string {
  // The prompt goes inside double quotes in bash and zsh, where these characters would be interpreted.
  if (/["$`!\\]/.test(prompt)) throw new Error(`CodingAgentBox prompt has a character the shell would interpret: ${prompt}`);
  return `${host.install}\n${host.launch} "${prompt.trim()}"`;
}
