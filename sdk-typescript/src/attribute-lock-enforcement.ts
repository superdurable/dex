// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { lockRequirementOf } from "./attribute-lock-requirement.js";
import { MissingAttributeLockError, type MissingAttributeLockAccess } from "./errors.js";
import type { RegisteredFlow } from "./flow.js";
import type { InvocationContext } from "./invocation-context.js";
import { Attribute, AttributeMap } from "./persistence.js";
import { Channel, ChannelMap } from "./wait.js";

/**
 * Throws MissingAttributeLockError when a Step method wrote or published without a declared lock.
 * Only the Step's registration-time locks count.
 */
export function requireRegisteredStepLocks(
  flow: RegisteredFlow,
  stepType: string,
  method: "waitFor" | "execute",
  context: InvocationContext,
): void {
  const lockKeys = flow.stepLockKeys.get(stepType);
  const heldLockKeys = (method === "waitFor" ? lockKeys?.waitFor : lockKeys?.execute) ?? new Set<string>();
  const missing = missingLockAccesses(flow, heldLockKeys, context);
  if (missing.length > 0) {
    throw new MissingAttributeLockError(flow.name, stepType, method, undefined, missing);
  }
}

/**
 * Throws MissingAttributeLockError when an RPC wrote or published without a declared lock.
 * Only `lockAttributes` of the RPC's options count; call-time locks never reach the Worker.
 */
export function requireRegisteredRPCLocks(
  flow: RegisteredFlow,
  rpcName: string,
  context: InvocationContext,
): void {
  const heldLockKeys = flow.rpcLockKeys.get(rpcName) ?? new Set<string>();
  const missing = missingLockAccesses(flow, heldLockKeys, context);
  if (missing.length > 0) {
    throw new MissingAttributeLockError(flow.name, undefined, "rpc", rpcName, missing);
  }
}

function missingLockAccesses(
  flow: RegisteredFlow,
  heldLockKeys: ReadonlySet<string>,
  context: InvocationContext,
): MissingAttributeLockAccess[] {
  const missing: MissingAttributeLockAccess[] = [];
  for (const write of context.getAttributeWrites()) {
    const { name, instance } = splitPhysicalName(write.key);
    const definition = flow.persistence.get(name);
    if (!(definition instanceof Attribute || definition instanceof AttributeMap)) {
      continue;
    }
    const requirement = lockRequirementOf(definition);
    if (requirement === undefined) {
      continue;
    }
    const isOwnKey = requirement.kind === "ownKey";
    const lockKey = isOwnKey ? write.key : requirement.lockAttribute.name;
    if (heldLockKeys.has(lockKey)) {
      continue;
    }
    missing.push({
      kind: definition instanceof AttributeMap ? "AttributeMap" : "Attribute",
      name,
      ...(instance === undefined ? {} : { instance }),
      lockAttributeName: isOwnKey ? name : requirement.lockAttribute.name,
      ...(isOwnKey && instance !== undefined ? { lockInstance: instance } : {}),
    });
  }
  const published = new Set<string>();
  for (const publication of context.getPublications()) {
    const physical = publication.channelName;
    if (published.has(physical)) {
      continue;
    }
    published.add(physical);
    const { name, instance } = splitPhysicalName(physical);
    const definition = flow.persistence.get(name);
    if (!(definition instanceof Channel || definition instanceof ChannelMap)) {
      continue;
    }
    const requirement = lockRequirementOf(definition);
    if (requirement?.kind !== "attribute" || heldLockKeys.has(requirement.lockAttribute.name)) {
      continue;
    }
    missing.push({
      kind: definition instanceof ChannelMap ? "ChannelMap" : "Channel",
      name,
      ...(instance === undefined ? {} : { instance }),
      lockAttributeName: requirement.lockAttribute.name,
    });
  }
  return missing;
}

/** Returns the definition name and the decoded map instance of a stored key. */
function splitPhysicalName(physical: string): { name: string; instance: string | undefined } {
  const separator = physical.indexOf("/");
  if (separator < 0) {
    return { name: physical, instance: undefined };
  }
  const escapedInstance = physical.slice(separator + 1);
  try {
    return { name: physical.slice(0, separator), instance: decodeURIComponent(escapedInstance) };
  } catch {
    return { name: physical.slice(0, separator), instance: escapedInstance };
  }
}
