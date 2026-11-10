// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** Marks a page, tab or link whose behavior may still change between releases. */
export function ExperimentalBadge({ className = '' }: { className?: string }) {
  return (
    <span className={`experimental-badge ${className}`.trim()} title="Experimental: this may change in a later release">
      Experimental
    </span>
  );
}
