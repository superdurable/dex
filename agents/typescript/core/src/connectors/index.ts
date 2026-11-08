// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

export {
  ConnectorToolSource,
  boundConnectorResult,
  classifyConnectorBranch,
  connectorResultTruncatedNote,
  connectorTool,
  defaultConnectorResultMaxBytes,
  defaultConnectorSourceName,
  formatConnectorResult,
  type ApprovalPredicate,
  type ConnectorBranchOutcome,
  type ConnectorInputValidator,
  type ConnectorManifestFactory,
  type ConnectorPackageManifest,
  type ConnectorTool,
  type ConnectorToolOptions,
} from "./connector-tool-source.js";
export { ConnectorStepContext, type ConnectorStepHeartbeats } from "./connector-step-context.js";
export { checkConnectorInputSchema, findConnectorInputViolation } from "./connector-input-schema.js";
