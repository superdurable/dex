// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

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
  schemaVersion: '1.0' | '2.0';
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
  groups?: FlowDefinitionGroup[];
  v2?: FlowV2Definition;
}

export interface FlowDefinitionGroup {
  id: string;
  label: string;
  stepIds: string[];
}

export interface FlowV2IndexedAttribute {
  attributeKey: string;
  indexKey: string;
  indexType: 'keyword' | 'fulltext' | 'keyword-array' | 'int' | 'double' | 'bool' | 'datetime';
  valueType: V2IndexedValueType;
  description: string;
}

export type V2ValueType =
  | 'string'
  | 'string-array'
  | 'int64'
  | 'double'
  | 'bool'
  | 'datetime'
  | 'json'
  | 'object'
  | 'array'
  | 'attribute-map';

export type V2IndexedValueType =
  | 'string'
  | 'string-array'
  | 'int64'
  | 'double'
  | 'bool'
  | 'datetime';

export type V2EditableValueType = Exclude<V2IndexedValueType, 'string-array'>;

export interface FlowV2Field {
  attributeKey: string;
  valueType: V2ValueType;
  editable: boolean;
  description: string;
}

export interface FlowV2View {
  rpcName: string;
  fields: FlowV2Field[];
}

export interface FlowV2ActionInputField {
  fieldName: string;
  valueType: V2EditableValueType;
  source: 'user' | 'attribute';
  attributeKey?: string;
  required: boolean;
  description: string;
}

export interface FlowV2Action {
  rpcName: string;
  label: string;
  condition: {
    attributeKey: string;
    operator: 'in';
    values: unknown[];
  };
  input: {
    kind: 'none' | 'object';
    fields?: FlowV2ActionInputField[];
  };
}

export interface FlowV2Definition {
  indexedAttributes: FlowV2IndexedAttribute[];
  summary: FlowV2View;
  display: FlowV2View;
  actions: FlowV2Action[];
}
