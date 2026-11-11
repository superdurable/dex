// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Link } from 'react-router-dom';
import type { FlowDefinitionEntry } from '@/lib/types';
import { CONNECTORS_PATH } from '../routes';
import { DesignCanvasErrorBoundary, DesignUnavailable } from './DesignCanvasErrorBoundary';
import { flowDefinitionLimitProblem, flowDefinitionShapeProblem } from './definitionLimits';
import { FlowDesignCanvas } from './FlowDesignCanvas';

/**
 * Draws one Flow Definition Graph: the Flow's Steps, stages and transitions, and nothing about any run.
 *
 * It reads only the definition it is given. It never loads history or stored values, so it explains
 * how a Flow is built rather than what one run did.
 */
export function FlowDesignGraph({ definition }: { definition: FlowDefinitionEntry }) {
  const shapeProblem = flowDefinitionShapeProblem(definition.graph);
  if (shapeProblem !== null) {
    return <DesignUnavailable title="This Flow design graph is malformed" detail={shapeProblem} />;
  }
  const problem = flowDefinitionLimitProblem(definition.graph);
  if (problem !== null) {
    return <DesignUnavailable title="This Flow design graph is too large to draw" detail={problem} />;
  }
  return (
    <DesignCanvasErrorBoundary key={definition.id}>
      <FlowDesignCanvas
        graph={definition.graph}
        graphKey={definition.id}
        connectorSettingsLink={<Link to={CONNECTORS_PATH}>Configure connection</Link>}
      />
    </DesignCanvasErrorBoundary>
  );
}
