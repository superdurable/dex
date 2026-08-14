// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

export function generatedSubFlowID(
  parentFlowID: string,
  stepExecutionID: string,
  index: number,
): string {
  if (!parentFlowID || !stepExecutionID) return '';
  return `SubFlow:${parentFlowID}-${stepExecutionID}-${index}`;
}
