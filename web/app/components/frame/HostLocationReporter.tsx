// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useMemo, useRef } from 'react';
import { useLocation } from 'react-router-dom';
import { postHostLocation, readHasHostNavigation } from '@/lib/hostNavigation';

/** Tells the host page about every route change, so it can keep the page in its own URL. */
export function HostLocationReporter() {
  const hasHostNavigation = useMemo(readHasHostNavigation, []);
  const { pathname, search } = useLocation();
  // StrictMode runs effects twice; the host needs each location once.
  const lastPostedLocation = useRef<string | null>(null);
  useEffect(() => {
    if (!hasHostNavigation) return;
    const location = `${pathname}${search}`;
    if (lastPostedLocation.current === location) return;
    lastPostedLocation.current = location;
    postHostLocation(pathname, search);
  }, [hasHostNavigation, pathname, search]);
  return null;
}
