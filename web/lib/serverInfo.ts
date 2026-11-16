// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { webURL } from './basePath';
import { readResponseJSON } from './http';
import type { ServerEngine } from './types';

/** What GET /api/server-info returns; protocol 0 means the server did not report one. */
export interface ServerInfo {
  serverEngine: ServerEngine;
  currentProtocolVersion: number;
}

/** The first server protocol with Clone Fork, the Event ID time travel point, and SubFlow reset policies. */
export const RUN_HISTORY_FORK_PROTOCOL_VERSION = 6;

/** True when the server supports Clone Fork, Event ID time travel, and SubFlow reset policies. */
export function supportsRunHistoryForks(currentProtocolVersion: number): boolean {
  return currentProtocolVersion >= RUN_HISTORY_FORK_PROTOCOL_VERSION;
}

/** Reads the server's protocol version; any failure reads as 0, so newer options stay hidden. */
export async function fetchServerProtocolVersion(signal?: AbortSignal): Promise<number> {
  try {
    const response = await fetch(webURL('/api/server-info'), { cache: 'no-store', signal });
    const value = await readResponseJSON<ServerInfo>(response);
    const version = value.currentProtocolVersion;
    return typeof version === 'number' && Number.isSafeInteger(version) && version > 0 ? version : 0;
  } catch {
    return 0;
  }
}
