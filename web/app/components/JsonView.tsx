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
