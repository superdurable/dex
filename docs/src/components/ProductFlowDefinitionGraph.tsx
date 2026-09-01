// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {useEffect, useState} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import {
  FlowDefinitionGraphView,
  type FlowDefinitionGraph,
} from '@superdurable/flow-definition-renderer';

interface ProductFlowDefinitionGraphProps {
  graph: FlowDefinitionGraph;
}

export default function ProductFlowDefinitionGraph({graph}: ProductFlowDefinitionGraphProps) {
  const [isExpanded, setIsExpanded] = useState(false);
  const {i18n} = useDocusaurusContext();
  const isChinese = i18n.currentLocale === 'zh-Hans';

  useEffect(() => {
    if (!isExpanded) return undefined;
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setIsExpanded(false);
    };
    document.addEventListener('keydown', closeOnEscape);
    return () => document.removeEventListener('keydown', closeOnEscape);
  }, [isExpanded]);

  return (
    <div
      aria-label={isExpanded ? graph.flow.name : undefined}
      aria-modal={isExpanded || undefined}
      className={`product-flow-definition-graph${isExpanded ? ' is-expanded' : ''}`}
      role={isExpanded ? 'dialog' : undefined}
    >
      <div className="product-flow-definition-graph-actions">
        <button
          aria-expanded={isExpanded}
          onClick={() => setIsExpanded((expanded) => !expanded)}
          type="button"
        >
          {isExpanded
            ? (isChinese ? '关闭完整 diagram' : 'Close full diagram')
            : (isChinese ? '展开 diagram' : 'Expand diagram')}
        </button>
      </div>
      <FlowDefinitionGraphView displayName={graph.flow.name} graph={graph} />
    </div>
  );
}
