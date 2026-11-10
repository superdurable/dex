// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type {FlowDefinitionGraph} from '@superdurable/flow-definition-renderer';
import {FlowDesignCanvas} from '@dex-web-design/FlowDesignCanvas';

/** A Flow definition drawn exactly as Dex Web's Design tab draws it, without a run. */
export default function DocsFlowCanvas({graph}: {graph: FlowDefinitionGraph}) {
  return (
    <div className="docs-flow-canvas">
      <FlowDesignCanvas graph={graph} graphKey={`${graph.source.path}#${graph.flow.name}`} />
    </div>
  );
}
