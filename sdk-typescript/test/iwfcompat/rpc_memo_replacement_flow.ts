// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { RpcFlow } from "./rpc_flow.js";

export class RpcMemoReplacementFlow extends RpcFlow {
  public override getFlowType(): string {
    return "RpcMemoReplacementFlow";
  }
}
