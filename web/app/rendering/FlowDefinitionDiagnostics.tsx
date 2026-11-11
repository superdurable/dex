// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowDefinitionEntry } from '@/lib/types';

/** The diagnostics of one definition file, one compact line each, or nothing when it has none. */
export function FlowDefinitionDiagnostics({ definition }: { definition: FlowDefinitionEntry }) {
  // The server rejects malformed diagnostics; a reader that skips them still never crashes on one.
  const diagnostics = (Array.isArray(definition.graph.diagnostics) ? definition.graph.diagnostics : [])
    .filter((diagnostic) => typeof diagnostic === 'object' && diagnostic !== null);
  if (diagnostics.length === 0) return null;
  return (
    <div aria-label="Definition diagnostics" className="flow-definition-diagnostics" role="list">
      {diagnostics.map((diagnostic, index) => (
        <div
          className={`flow-definition-diagnostic is-${diagnostic.severity === 'error' ? 'error' : 'warning'}`}
          key={`${diagnostic.code}-${index}`}
          role="listitem"
        >
          <b>{diagnostic.code}</b>
          <span>{diagnostic.message}</span>
        </div>
      ))}
    </div>
  );
}
