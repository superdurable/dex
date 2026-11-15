// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  FlowCloneForkPointType as ProtoFlowCloneForkPointType,
  FlowResetType as ProtoFlowResetType,
  SubFlowResetPolicy as ProtoSubFlowResetPolicy,
} from "./gen/dex.js";
import {
  SubFlowResetPolicy,
  TimeTravelStepMethod,
  TimeTravelType,
} from "./options.js";

export const cloneForkFlowMinimumServerProtocolVersion: number = 6;
const cloneForkFlowMinimumServerRelease = "server-zd 0.1.33";

/** The point fields that TimeTravelOptions and CloneForkFlowOptions share. */
export interface HistoryPointSelection {
  readonly type: TimeTravelType;
  readonly historyEventTime?: Date | undefined;
  readonly stepType?: string | undefined;
  readonly stepExecutionId?: string | undefined;
  readonly stepMethod?: TimeTravelStepMethod | undefined;
  readonly eventId?: number | undefined;
}

export function validateHistoryPointSelection(operation: string, point: HistoryPointSelection): void {
  const hasHistoryEventTime = point.historyEventTime !== undefined;
  const hasStepType = (point.stepType ?? "") !== "";
  const hasStepExecutionId = (point.stepExecutionId ?? "") !== "";
  const hasStepMethod = point.stepMethod !== undefined;
  const hasEventId = point.eventId !== undefined && point.eventId !== 0;
  const others = (excluded: readonly boolean[]): boolean => excluded.some((isSet) => isSet);
  switch (point.type) {
    case TimeTravelType.BEGINNING:
    case TimeTravelType.CURRENT:
      if (others([hasHistoryEventTime, hasStepType, hasStepExecutionId, hasStepMethod, hasEventId])) {
        throw new TypeError(`${operation} type ${point.type} cannot include another selector`);
      }
      return;
    case TimeTravelType.HISTORY_EVENT_TIME:
      if (!hasHistoryEventTime || Number.isNaN(point.historyEventTime!.getTime())) {
        throw new TypeError(`${operation} historyEventTime must be a valid Date`);
      }
      if (others([hasStepType, hasStepExecutionId, hasStepMethod, hasEventId])) {
        throw new TypeError(`${operation} historyEventTime cannot be combined with another selector`);
      }
      return;
    case TimeTravelType.STEP_TYPE:
      if (!hasStepType) {
        throw new TypeError(`${operation} stepType must not be empty`);
      }
      if (others([hasHistoryEventTime, hasStepExecutionId, hasStepMethod, hasEventId])) {
        throw new TypeError(`${operation} stepType cannot be combined with another selector`);
      }
      return;
    case TimeTravelType.STEP_EXECUTION_ID:
      if (!hasStepExecutionId) {
        throw new TypeError(`${operation} stepExecutionId must not be empty`);
      }
      if (others([hasHistoryEventTime, hasStepType, hasEventId])) {
        throw new TypeError(`${operation} stepExecutionId cannot be combined with another selector`);
      }
      if (point.stepMethod !== TimeTravelStepMethod.WAIT_FOR && point.stepMethod !== TimeTravelStepMethod.EXECUTE) {
        throw new TypeError(`${operation} stepMethod must be WAIT_FOR or EXECUTE`);
      }
      return;
    case TimeTravelType.EVENT_ID:
      if (point.eventId === undefined || !Number.isSafeInteger(point.eventId) || point.eventId <= 0) {
        throw new TypeError(`${operation} eventId must be a positive safe integer`);
      }
      if (others([hasHistoryEventTime, hasStepType, hasStepExecutionId, hasStepMethod])) {
        throw new TypeError(`${operation} eventId cannot be combined with another selector`);
      }
      return;
    default:
      throw new TypeError(`unsupported ${operation} type ${String(point.type)}`);
  }
}

export function mapTimeTravelType(type: TimeTravelType): ProtoFlowResetType {
  switch (type) {
    case TimeTravelType.BEGINNING:
      return ProtoFlowResetType.FLOW_RESET_TYPE_BEGINNING;
    case TimeTravelType.HISTORY_EVENT_TIME:
      return ProtoFlowResetType.FLOW_RESET_TYPE_HISTORY_EVENT_TIME;
    case TimeTravelType.STEP_TYPE:
      return ProtoFlowResetType.FLOW_RESET_TYPE_STEP_TYPE;
    case TimeTravelType.STEP_EXECUTION_ID:
      return ProtoFlowResetType.FLOW_RESET_TYPE_STEP_EXECUTION_ID;
    case TimeTravelType.EVENT_ID:
      return ProtoFlowResetType.FLOW_RESET_TYPE_EVENT_ID;
    case TimeTravelType.CURRENT:
      throw new TypeError("time travel type CURRENT is valid only for cloneForkFlow");
    default:
      throw new TypeError(`unsupported time travel type ${String(type)}`);
  }
}

export function mapCloneForkPointType(type: TimeTravelType): ProtoFlowCloneForkPointType {
  switch (type) {
    case TimeTravelType.CURRENT:
      return ProtoFlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_CURRENT;
    case TimeTravelType.BEGINNING:
      return ProtoFlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_BEGINNING;
    case TimeTravelType.HISTORY_EVENT_TIME:
      return ProtoFlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_HISTORY_EVENT_TIME;
    case TimeTravelType.STEP_TYPE:
      return ProtoFlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_STEP_TYPE;
    case TimeTravelType.STEP_EXECUTION_ID:
      return ProtoFlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_STEP_EXECUTION_ID;
    case TimeTravelType.EVENT_ID:
      return ProtoFlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_EVENT_ID;
    default:
      throw new TypeError(`unsupported clone fork type ${String(type)}`);
  }
}

export function mapSubFlowResetPolicy(policy: SubFlowResetPolicy | undefined): ProtoSubFlowResetPolicy {
  switch (policy) {
    case undefined:
    case SubFlowResetPolicy.DEFAULT:
      return ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_UNSPECIFIED;
    case SubFlowResetPolicy.ALWAYS_RESTART:
      return ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ALWAYS_RESTART;
    case SubFlowResetPolicy.ALWAYS_FAIL:
      return ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ALWAYS_FAIL;
    case SubFlowResetPolicy.ALWAYS_REJECT:
      return ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ALWAYS_REJECT;
    case SubFlowResetPolicy.ATTACH_ELSE_RESTART:
      return ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_RESTART;
    case SubFlowResetPolicy.ATTACH_ELSE_FAIL:
      return ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_FAIL;
    case SubFlowResetPolicy.ATTACH_ELSE_REJECT:
      return ProtoSubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_REJECT;
    default:
      throw new TypeError(`unsupported SubFlow reset policy ${String(policy)}`);
  }
}

// An older Server answers UNIMPLEMENTED, so fail first with the upgrade to make.
export function requireCloneForkFlowSupport(negotiatedServerProtocolVersion: number): void {
  if (negotiatedServerProtocolVersion >= cloneForkFlowMinimumServerProtocolVersion) {
    return;
  }
  throw new Error(
    `cloneForkFlow requires Server protocol version ${cloneForkFlowMinimumServerProtocolVersion} or later `
      + `(${cloneForkFlowMinimumServerRelease} or later); negotiated protocol version is `
      + `${negotiatedServerProtocolVersion}. Upgrade the Server.`,
  );
}
