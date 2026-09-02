// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

export interface SourceSpan {
  startLine: number;
  startColumn: number;
  endLine: number;
  endColumn: number;
}

export interface FlowDefinitionNode {
  id: string;
  kind: string;
  name: string;
  parentId?: string;
  condition?: string;
  phase?: string;
  start?: boolean;
  external?: boolean;
  span?: SourceSpan;
  resource?: {
    valueType: string;
    map?: boolean;
  };
  wait?: {
    type: string;
    conditions: Array<{
      kind: 'channel' | 'timer' | 'subflow' | 'unknown';
      label: string;
      resourceId?: string;
      subFlowId?: string;
      expression?: string;
      span?: SourceSpan;
    }>;
  };
  decision?: {
    type: string;
    checkedChannels?: string[];
    cancellations?: Array<{
      stepId: string;
      scope: 'all' | 'siblings';
    }>;
  };
  metadata?: Record<string, unknown>;
}

export interface FlowDefinitionEdge {
  id: string;
  kind: string;
  from: string;
  to: string;
  label?: string;
  condition?: string;
  multiplicity?: string;
  span?: SourceSpan;
  metadata?: Record<string, unknown>;
}

export interface FlowDefinitionDiagnostic {
  severity: 'warning' | 'error';
  code: string;
  message: string;
  span?: SourceSpan;
}

export interface FlowDefinitionGraph {
  schemaVersion: '1.0';
  valid: boolean;
  source: {
    language: 'go' | 'python';
    path: string;
  };
  flow: {
    name: string;
    startStepId?: string;
    span?: SourceSpan;
  };
  nodes: FlowDefinitionNode[];
  edges: FlowDefinitionEdge[];
  diagnostics: FlowDefinitionDiagnostic[];
}
