// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {useState, type CSSProperties} from 'react';
import type {FlowDefinitionGraph} from '@superdurable/flow-definition-renderer';
import {FlowDesignCanvas} from '@dex-web-design/FlowDesignCanvas';

/** Inline diagrams open no larger than the cards' drawn size, so a small Flow gets a small canvas. */
const INLINE_MAX_ZOOM = 1;

interface DocsFlowCanvasProps {
  graph: FlowDefinitionGraph;
  /** True in the full-screen dialog, which keeps Dex Web's own sizing and zoom. */
  isExpanded: boolean;
}

/** A Flow definition drawn exactly as Dex Web's Design tab draws it, without a run. */
export default function DocsFlowCanvas({graph, isExpanded}: DocsFlowCanvasProps) {
  const [preferredHeightPx, setPreferredHeightPx] = useState<number | null>(null);
  const style =
    preferredHeightPx === null
      ? undefined
      : ({'--docs-flow-canvas-height': `${preferredHeightPx}px`} as CSSProperties);
  return (
    <div className="docs-flow-canvas" style={style}>
      <FlowDesignCanvas
        graph={graph}
        graphKey={`${graph.source.path}#${graph.flow.name}`}
        wholeMaxZoom={isExpanded ? undefined : INLINE_MAX_ZOOM}
        onPreferredHeight={isExpanded ? undefined : setPreferredHeightPx}
      />
    </div>
  );
}
