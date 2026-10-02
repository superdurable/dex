// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

export function LoadingIndicator({
  label,
  className = 'page-loading',
}: {
  label: string;
  className?: string;
}) {
  return (
    <div className={className} role="status">
      <span className="loading-spinner" aria-hidden="true" />
      <span>{label}</span>
    </div>
  );
}
