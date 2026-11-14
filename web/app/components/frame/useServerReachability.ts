// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useState } from 'react';
import { webURL } from '@/lib/basePath';

export type ServerReachability = 'checking' | 'reachable' | 'unreachable';

/** The latest readiness answer, with the detail a tooltip names. */
export interface ServerReachabilityStatus {
  state: ServerReachability;
  /** The last failure's message, while the Dex Server is unreachable. */
  lastError?: string;
  /** When the readiness probe last succeeded, in milliseconds since the epoch. */
  lastReachableAt?: number;
}

const RECHECK_MILLISECONDS = 30_000;

/** Reads the error message of a failed probe response, or its HTTP status. */
async function probeErrorOf(response: Response): Promise<string> {
  try {
    const body = await response.json() as { error?: unknown };
    if (typeof body.error === 'string' && body.error) return body.error;
  } catch {
    // A body that is not JSON falls back to the HTTP status.
  }
  return `HTTP ${response.status}`;
}

/** Asks the readiness probe whether Dex Web reaches the Dex Server, now and every 30 s. */
export function useServerReachability(pathname: string): ServerReachabilityStatus {
  const [status, setStatus] = useState<ServerReachabilityStatus>({ state: 'checking' });
  useEffect(() => {
    let isActive = true;
    const markUnreachable = (lastError: string) => {
      if (isActive) setStatus((current) => ({ state: 'unreachable', lastError, lastReachableAt: current.lastReachableAt }));
    };
    const check = () => {
      fetch(webURL('/readyz'), { cache: 'no-store' })
        .then(async (response) => {
          if (response.ok) {
            if (isActive) setStatus({ state: 'reachable', lastReachableAt: Date.now() });
            return;
          }
          markUnreachable(await probeErrorOf(response));
        })
        .catch((probeError: unknown) => {
          markUnreachable(probeError instanceof Error ? probeError.message : 'The readiness probe failed');
        });
    };
    check();
    const timer = window.setInterval(check, RECHECK_MILLISECONDS);
    return () => {
      isActive = false;
      window.clearInterval(timer);
    };
  }, [pathname]);
  return status;
}
