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

const RECHECK_MILLISECONDS = 30_000;

/** Asks the readiness probe whether Dex Web reaches the Dex Server, now and every 30 s. */
export function useServerReachability(pathname: string): ServerReachability {
  const [reachability, setReachability] = useState<ServerReachability>('checking');
  useEffect(() => {
    let isActive = true;
    const check = () => {
      fetch(webURL('/readyz'), { cache: 'no-store' })
        .then((response) => { if (isActive) setReachability(response.ok ? 'reachable' : 'unreachable'); })
        .catch(() => { if (isActive) setReachability('unreachable'); });
    };
    check();
    const timer = window.setInterval(check, RECHECK_MILLISECONDS);
    return () => {
      isActive = false;
      window.clearInterval(timer);
    };
  }, [pathname]);
  return reachability;
}
