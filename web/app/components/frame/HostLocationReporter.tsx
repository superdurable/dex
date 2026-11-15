// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useMemo } from 'react';
import { useLocation } from 'react-router-dom';
import { postHostLocation, readHasHostNavigation } from '@/lib/hostNavigation';

/** Tells the host page about every route change, so it can keep the page in its own URL. */
export function HostLocationReporter() {
  const hasHostNavigation = useMemo(readHasHostNavigation, []);
  const { pathname, search } = useLocation();
  useEffect(() => {
    if (hasHostNavigation) postHostLocation(pathname, search);
  }, [hasHostNavigation, pathname, search]);
  return null;
}
