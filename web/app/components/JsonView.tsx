// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

'use client';

import { useState } from 'react';

export function JsonView({
  value,
  label = 'Details',
  initiallyOpen = false,
}: {
  value: unknown;
  label?: string;
  initiallyOpen?: boolean;
}) {
  const [open, setOpen] = useState(initiallyOpen);
  return (
    <div className="json-view">
      <button type="button" className="json-toggle" onClick={() => setOpen(!open)}>
        <span>{open ? '−' : '+'}</span>
        {label}
      </button>
      {open && <pre>{JSON.stringify(value, null, 2)}</pre>}
    </div>
  );
}
