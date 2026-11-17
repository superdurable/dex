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
  /** Since Dex Web 1.45.11; 0 when the server did not report one. */
  minimumSupportedProtocolVersion?: number;
  /** Since Dex Web 1.45.11; empty when the server did not report one. */
  serverVersion?: string;
  /** Since Dex Web 1.45.11; the build that serves Dex Web, such as "dex-web 1.45.11"; empty when unknown. */
  webVersion?: string;
}

/** The server info that Dex Web shows, with every unknown field left out. */
export interface ServerInfoDisplay {
  engineLabel?: string;
  serverVersion?: string;
  minimumProtocolVersion?: number;
  currentProtocolVersion?: number;
  webVersion?: string;
}

const ENGINE_LABEL: Partial<Record<ServerEngine, string>> = { zd: 'server-zd', temporal: 'Temporal', cadence: 'Cadence' };

function positiveVersionOf(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : undefined;
}

function textOf(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() !== '' ? value.trim() : undefined;
}

/** Reads the server info Dex Web shows; any failure or malformed field reads as unknown. */
export async function fetchServerInfoDisplay(signal?: AbortSignal): Promise<ServerInfoDisplay> {
  try {
    const response = await fetch(webURL('/api/server-info'), { cache: 'no-store', signal });
    const value = await readResponseJSON<Partial<Record<keyof ServerInfo, unknown>>>(response);
    return {
      engineLabel: typeof value.serverEngine === 'string' ? ENGINE_LABEL[value.serverEngine as ServerEngine] : undefined,
      serverVersion: textOf(value.serverVersion),
      minimumProtocolVersion: positiveVersionOf(value.minimumSupportedProtocolVersion),
      currentProtocolVersion: positiveVersionOf(value.currentProtocolVersion),
      webVersion: textOf(value.webVersion),
    };
  } catch {
    return {};
  }
}

/** The protocol range, such as "1–7", or the current protocol alone; undefined when the server reports none. */
export function serverProtocolRangeOf(info: ServerInfoDisplay): string | undefined {
  const { minimumProtocolVersion: minimum, currentProtocolVersion: current } = info;
  if (current === undefined) return undefined;
  return minimum === undefined || minimum >= current ? `${current}` : `${minimum}–${current}`;
}

/** A one-line summary, such as "server-zd 0.1.34 · protocol 7"; undefined when nothing is known. */
export function serverInfoSummaryOf(info: ServerInfoDisplay): string | undefined {
  const name = [info.engineLabel, info.serverVersion].filter(Boolean).join(' ');
  const protocol = info.currentProtocolVersion === undefined ? '' : `protocol ${info.currentProtocolVersion}`;
  const summary = [name, protocol].filter(Boolean).join(' · ');
  return summary || undefined;
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
