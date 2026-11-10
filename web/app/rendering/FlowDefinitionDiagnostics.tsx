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
  const { diagnostics } = definition.graph;
  if (diagnostics.length === 0) return null;
  return (
    <div aria-label="Definition diagnostics" className="flow-definition-diagnostics" role="list">
      {diagnostics.map((diagnostic, index) => (
        <div
          className={`flow-definition-diagnostic is-${diagnostic.severity}`}
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
