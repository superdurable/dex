// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowDefinitionNode } from './types';

export function SubFlowDefinitionLink({ definition, href }: { definition: FlowDefinitionNode; href?: string }) {
  return href ? <a className="nodrag" href={href}>{definition.name}</a> : <strong>{definition.name}</strong>;
}
