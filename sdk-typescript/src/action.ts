// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { isHardDeleteOnNullDefinition } from "./attribute-hard-delete.js";
import { AttributeMatch } from "./attribute-match.js";
import { isAttributeStoreSynced } from "./attribute-store-sync.js";
import {
  AttributeMatchOperator as ProtoAttributeMatchOperator,
  IndexType as ProtoIndexType,
  type ActionPermissionMapping,
  type ActionPermissionMappings,
  type AttributeWrite,
  type Value as ProtoValue,
} from "./gen/dex.js";
import { Attribute, AttributeMap } from "./persistence.js";

/**
 * The Search Attribute that holds the permissions of Actions that have been available in a Flow execution.
 *
 * Dex Web Work Queue filters on it when an operator chooses **Working as** one permission.
 * The Server maintains it from the Action permission mappings that Workers send.
 * Applications never write it. The index is a discovery projection, not an authorization result:
 * an Action gateway must still authorize the selected RPC against its required permission.
 *
 * @example
 * ```ts
 * const query = `${WORK_QUEUE_PERMISSIONS_INDEX_KEY} = "refund.approve"`;
 * ```
 * @since 1.39.0
 */
export const WORK_QUEUE_PERMISSIONS_INDEX_KEY = "DexWorkQueuePermissions";

const actionPermissionPattern = /^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$/;

/**
 * The availability condition of an {@link Action}, created by {@link whenAttributeMatches}.
 *
 * A condition names one scalar Attribute and the values that make the Action available.
 * The {@link Registry} accepts only conditions built from a registered Attribute and equality matches.
 *
 * @example
 * ```ts
 * const awaitingManager = whenAttributeMatches(caseStatus, AttributeMatch.equalTo("awaiting-manager"));
 * ```
 * @since 1.39.0
 */
export class ActionCondition {
  /**
   * Creates a condition. Prefer {@link whenAttributeMatches}, which calls this constructor.
   * @param attribute - The scalar Attribute whose value decides availability.
   * @param matches - The equality matches; any one of them makes the Action available.
   */
  public constructor(
    /** The scalar Attribute whose value decides availability. */
    public readonly attribute: Attribute<any>,
    /** The equality matches; any one of them makes the Action available. */
    public readonly matches: readonly AttributeMatch<any>[],
  ) {
    Object.freeze(this);
  }
}

/**
 * Creates an Action condition for one scalar Attribute.
 *
 * At least one match is required. Every match must be created with {@link AttributeMatch.equalTo}.
 * Multiple matches use OR semantics. A missing or deleted Attribute does not match.
 * Supported value types are strings, booleans, bigint integers, and finite numbers.
 * The Attribute must be registered in the same Flow as the Action RPC, and must not use
 * {@link Attribute.hardDeleteOnNull}. The {@link Registry} validates these rules when it is constructed.
 *
 * @example
 * ```ts
 * const caseStatus = new Attribute("case-status", stringCodec);
 * const condition = whenAttributeMatches(
 *   caseStatus,
 *   AttributeMatch.equalTo("awaiting-manager"),
 *   AttributeMatch.equalTo("escalated"),
 * );
 * ```
 * @typeParam T - Attribute value type.
 * @param attribute - The scalar Attribute definition registered by the Flow.
 * @param matches - One or more equality matches.
 * @returns A condition to pass as {@link ActionOptions.condition}.
 * @throws {TypeError} When `attribute` is not an {@link Attribute} definition.
 * @since 1.39.0
 */
export function whenAttributeMatches<T>(
  attribute: Attribute<T>,
  ...matches: AttributeMatch<T>[]
): ActionCondition {
  if (!(attribute instanceof Attribute)) {
    throw new TypeError("whenAttributeMatches requires a scalar Attribute definition");
  }
  return new ActionCondition(attribute, Object.freeze([...matches]));
}

/**
 * The settings of an {@link Action}.
 * @since 1.39.0
 */
export interface ActionOptions {
  /** The user-facing label that Dex Web shows on the Action's button. Must not be blank. */
  readonly label: string;
  /**
   * The single permission required by the Action, such as `"refund.approve"`.
   * Permissions use lowercase letter and number segments separated by dots or hyphens.
   * Several Actions of a Flow may share one permission; the Work Queue projection removes duplicates.
   */
  readonly requiredPermission: string;
  /**
   * When the Action is available, created with {@link whenAttributeMatches}.
   * Omit it when the RPC checks current state itself. An Action without a condition sends no permission
   * mapping, so its permission enters {@link WORK_QUEUE_PERMISSIONS_INDEX_KEY} only through another Action.
   */
  readonly condition?: ActionCondition;
}

/**
 * Declares an RPC as an Action that a person can invoke from Dex Web.
 *
 * Pass an Action as {@link RPCOptions.action} to {@link rpc} or {@link declareRPCMethod}.
 * The definition is immutable. The {@link Registry} validates it when it is constructed.
 *
 * When a Step, timeout handler, or RPC of the Flow writes a condition Attribute, the Worker sends the
 * permission mapping of every Action with a condition. The Server then records the permissions of
 * matching Actions in {@link WORK_QUEUE_PERMISSIONS_INDEX_KEY}. `startFlow` and SubFlow starts record the
 * permissions that the initial Attributes make available.
 *
 * `dexcli visualize` reads the Action statically. Write `new Action({...})` directly in the RPC options,
 * with literal or module-level constant strings.
 *
 * The condition only controls presentation and Work Queue discovery. It does not authorize the call.
 * The RPC must read current state again, under a lock when the check and its write must be atomic.
 *
 * @example
 * ```ts
 * const caseStatus = new Attribute("case-status", stringCodec);
 *
 * class RefundFlow implements Flow<void> {
 *   @rpc({
 *     lockAttributes: [caseStatus.lock()],
 *     action: new Action({
 *       label: "Approve refund",
 *       requiredPermission: "refund.approve",
 *       condition: whenAttributeMatches(caseStatus, AttributeMatch.equalTo("awaiting-manager")),
 *     }),
 *   })
 *   approveRefund(context: Context): RPCResult<void> {
 *     if (caseStatus.get(context) !== "awaiting-manager") {
 *       throw new Error("the refund is no longer awaiting approval");
 *     }
 *     caseStatus.set(context, "approved");
 *     return { output: undefined };
 *   }
 * }
 * ```
 * @since 1.39.0
 */
export class Action {
  /** The user-facing label. */
  public readonly label: string;
  /** The single permission required by the Action. */
  public readonly requiredPermission: string;
  /** The availability condition, or `undefined` when the RPC checks state itself. */
  public readonly condition: ActionCondition | undefined;

  /**
   * Creates an Action definition.
   * @param options - The label, required permission, and optional condition.
   */
  public constructor(options: ActionOptions) {
    this.label = options.label;
    this.requiredPermission = options.requiredPermission;
    this.condition = options.condition;
    Object.freeze(this);
  }
}

/** A validated Action of a registered Flow. */
export interface RegisteredAction {
  readonly rpcName: string;
  readonly label: string;
  readonly requiredPermission: string;
  readonly condition?: {
    readonly attributeKey: string;
    readonly operands: readonly ProtoValue[];
  };
}

type PersistenceDefinition = { readonly name: string };

export function compileAction(
  rpcName: string,
  action: unknown,
  persistence: ReadonlyMap<string, PersistenceDefinition>,
  flowName: string,
): RegisteredAction {
  const prefix = `RPC ${rpcName} Action`;
  if (!(action instanceof Action)) {
    throw new TypeError(`${prefix} must be created with new Action`);
  }
  if (typeof action.label !== "string" || action.label.trim() === "") {
    throw new TypeError(`${prefix} label must not be empty`);
  }
  if (typeof action.requiredPermission !== "string" || !actionPermissionPattern.test(action.requiredPermission)) {
    throw new TypeError(
      `${prefix} permission ${JSON.stringify(action.requiredPermission)} must use lowercase letter and number segments separated by dots or hyphens`,
    );
  }
  const registered = { rpcName, label: action.label, requiredPermission: action.requiredPermission };
  if (action.condition === undefined) {
    return Object.freeze(registered);
  }
  if (!(action.condition instanceof ActionCondition)) {
    throw new TypeError(`${prefix} condition must be created with whenAttributeMatches`);
  }
  const attribute = action.condition.attribute;
  const attributeName = attribute.name;
  if (action.condition.matches.length === 0) {
    throw new TypeError(`${prefix}: condition Attribute ${JSON.stringify(attributeName)} requires at least one match`);
  }
  const declared = persistence.get(attributeName);
  if (!(declared instanceof Attribute) || declared instanceof AttributeMap) {
    throw new TypeError(
      `${prefix}: condition Attribute ${JSON.stringify(attributeName)} is not declared by Flow ${JSON.stringify(flowName)}`,
    );
  }
  if (isHardDeleteOnNullDefinition(declared) || isHardDeleteOnNullDefinition(attribute)) {
    throw new TypeError(
      `${prefix}: condition Attribute ${JSON.stringify(attributeName)} uses hardDeleteOnNull, which conditions cannot read`,
    );
  }
  if (declared !== attribute && !isSameAttributeDefinition(declared, attribute)) {
    throw new TypeError(
      `${prefix}: condition Attribute ${JSON.stringify(attributeName)} does not match its registered definition`,
    );
  }
  const operands = action.condition.matches.map((match) => {
    if (!(match instanceof AttributeMatch)) {
      throw new TypeError(`${prefix}: condition matches must be created with AttributeMatch.equalTo`);
    }
    let encoded;
    try {
      encoded = match.encode(declared.codec);
    } catch (failure) {
      throw new TypeError(
        `${prefix}: condition Attribute ${JSON.stringify(attributeName)}: ${failure instanceof Error ? failure.message : String(failure)}`,
      );
    }
    if (encoded.operator !== ProtoAttributeMatchOperator.ATTRIBUTE_MATCH_OPERATOR_EQUAL) {
      throw new TypeError(
        `${prefix}: condition Attribute ${JSON.stringify(attributeName)} supports only AttributeMatch.equalTo`,
      );
    }
    return encoded.operand;
  });
  return Object.freeze({
    ...registered,
    condition: Object.freeze({ attributeKey: attributeName, operands: Object.freeze(operands) }),
  });
}

function isSameAttributeDefinition(left: Attribute<unknown>, right: Attribute<unknown>): boolean {
  return (
    left.codec === right.codec &&
    JSON.stringify(left.index ?? null) === JSON.stringify(right.index ?? null) &&
    isAttributeStoreSynced(left) === isAttributeStoreSynced(right)
  );
}

export function actionPermissionMappingsForWrites(
  actions: readonly RegisteredAction[],
  writes: readonly AttributeWrite[],
): ActionPermissionMappings | undefined {
  const sourceKeys = new Set(
    actions.flatMap((action) => (action.condition === undefined ? [] : [action.condition.attributeKey])),
  );
  if (sourceKeys.size === 0 || !writes.some((write) => sourceKeys.has(write.key))) {
    return undefined;
  }
  const mappings: ActionPermissionMapping[] = [];
  for (const action of actions) {
    if (action.condition !== undefined) {
      mappings.push({
        attributeKey: action.condition.attributeKey,
        equalValues: [...action.condition.operands],
        requiredPermission: action.requiredPermission,
      });
    }
  }
  return { mappings };
}

export function initialWorkQueuePermissionsWrite(
  actions: readonly RegisteredAction[],
  writes: readonly AttributeWrite[],
): AttributeWrite | undefined {
  const values = new Map<string, ProtoValue | undefined>();
  for (const write of writes) {
    values.set(write.key, write.value);
  }
  const permissions = new Set<string>();
  for (const action of actions) {
    const condition = action.condition;
    if (condition === undefined) {
      continue;
    }
    const value = values.get(condition.attributeKey);
    if (condition.operands.some((operand) => equalScalarValues(value, operand))) {
      permissions.add(action.requiredPermission);
    }
  }
  if (permissions.size === 0) {
    return undefined;
  }
  const sorted = [...permissions].sort((left, right) => (left < right ? -1 : left > right ? 1 : 0));
  return {
    key: WORK_QUEUE_PERMISSIONS_INDEX_KEY,
    value: {
      kind: {
        $case: "objValue",
        value: { encoding: "json", payload: new TextEncoder().encode(JSON.stringify(sorted)) },
      },
    },
    indexConfig: {
      enable: true,
      type: ProtoIndexType.INDEX_TYPE_KEYWORD_ARRAY,
      indexKey: WORK_QUEUE_PERMISSIONS_INDEX_KEY,
    },
    syncConfig: undefined,
    isHardDeleteOnNull: false,
  };
}

export function appendInitialWorkQueuePermissions(
  actions: readonly RegisteredAction[],
  writes: AttributeWrite[],
): AttributeWrite[] {
  const projection = initialWorkQueuePermissionsWrite(actions, writes);
  return projection === undefined ? writes : [...writes, projection];
}

function equalScalarValues(left: ProtoValue | undefined, right: ProtoValue): boolean {
  const leftKind = left?.kind;
  const rightKind = right.kind;
  if (leftKind === undefined || rightKind === undefined || leftKind.$case !== rightKind.$case) {
    return false;
  }
  switch (leftKind.$case) {
    case "stringValue":
    case "boolValue":
    case "intValue":
    case "doubleValue":
      return leftKind.value === (rightKind as typeof leftKind).value;
    default:
      return false;
  }
}
