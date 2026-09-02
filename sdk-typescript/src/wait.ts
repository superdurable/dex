// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { Codec } from "./codec.js";
import type { Context } from "./context.js";
import type { Flow } from "./flow.js";
import type { SubFlowOptions } from "./subflow.js";
import {
  requireConditionId,
  requireName,
  requirePersistenceDefinitionName,
  validateChannelBounds,
} from "./validation.js";

/** Describes one durable Timer, Channel, or SubFlow readiness condition. */
export interface Condition {
  /** Condition family interpreted by Dex. */
  readonly kind: "timer" | "channel" | "subFlow";
  /** Optional stable ID unique within the Wait tree. */
  readonly conditionId?: string;
  /** Timer delay in non-negative milliseconds. */
  readonly durationMs?: number;
  /** Physical Channel name for a Channel condition. */
  readonly channelName?: string;
  /** ChannelMap instance for a map condition. */
  readonly instance?: string;
  /** Inclusive lower queued-value bound. */
  readonly atLeast?: number;
  /** Inclusive upper queued-value bound. */
  readonly atMost?: number;
  /** Exact registered Flow instance targeted by a SubFlow condition. */
  readonly subFlow?: Flow<any>;
  /** Starting Step input for a SubFlow condition. */
  readonly subFlowInput?: unknown;
  /** Start, reuse, and condition options for a SubFlow condition. */
  readonly subFlowOptions?: SubFlowOptions;
}

/** Groups Conditions that must become ready together. */
export interface ConditionCombination {
  /** Ordered Conditions in this all-of group. */
  readonly conditions: readonly Condition[];
}

/** Creates all-of Condition groups for `Wait.anyCombinationOf`. */
export const ConditionCombination = Object.freeze({
  /**
   * Groups conditions that must all become ready together.
   * @param conditions - Conditions in stable evaluation order.
   * @returns A group with the conditions.
   */
  of(...conditions: readonly Condition[]): ConditionCombination {
    return { conditions };
  },
});

/** Creates durable Timer conditions without blocking a worker thread. */
export const Timer = Object.freeze({
  /**
   * Creates a Timer ready after a non-negative delay.
   * @param durationMs - Durable delay in milliseconds.
   * @param conditionId - Optional stable ID used by skip-Timer APIs.
   * @returns A Timer condition accepted by Wait factories.
   */
  byDuration(durationMs: number, conditionId?: string): Condition {
    if (durationMs < 0 || !Number.isFinite(durationMs)) {
      throw new RangeError("timer duration must be non-negative");
    }
    requireConditionId(conditionId);
    return {
      kind: "timer",
      durationMs,
      ...(conditionId === undefined ? {} : { conditionId }),
    };
  },
});

/**
 * Identifies one typed value that is still pending in a Channel.
 * @typeParam T - Decoded Channel value type.
 */
export interface ChannelMessage<T> {
  /** UUIDv7 assigned by Dex when the message was published. */
  readonly messageId: string;
  /** Decoded Channel value. */
  readonly value: T;
}

/**
 * Defines a typed, durable singleton message stream owned by a Flow.
 * @typeParam T - Type of every published value.
 */
export class Channel<T> {
  /**
   * Creates a Channel definition.
   * @param name - Non-empty name without `/`, unique within the Flow.
   * @param codec - Element codec.
   */
  public constructor(
    public readonly name: string,
    public readonly codec: Codec<T>,
  ) {
    requirePersistenceDefinitionName(name);
  }

  /**
   * Stages one value to append with the current decision.
   * @param context - Current Step or RPC Context.
   * @param value - Typed value to append.
   */
  public publish(context: Context, value: T): void {
    context.publish(this, value);
  }

  /**
   * Stages deletion of one pending message from an RPC.
   * @param context - Current RPC Context.
   * @param messageId - Non-empty server-assigned message ID.
   */
  public delete(context: Context, messageId: string): void {
    context.deleteChannelMessage(this as Channel<unknown>, messageId);
  }

  /**
   * Returns the current queued value count.
   * @param context - Current Step or RPC Context.
   * @returns Non-negative queued value count.
   */
  public size(context: Context): number {
    return context.channelSize(this as Channel<unknown>);
  }

  /**
   * Returns values selected by this Step's satisfied condition.
   * @param context - Current Step Context.
   * @returns Ordered values, or an empty array when this Channel was not selected.
   */
  public results(context: Context): readonly T[] {
    return context.channelResults(this);
  }

  /**
   * Creates a condition consuming exactly one value.
   * @param conditionId - Optional stable condition identifier.
   * @returns An exact-one Channel condition.
   */
  public forOne(conditionId?: string): Condition {
    return this.range(1, 1, conditionId);
  }

  /**
   * Creates a condition consuming exactly `count` values.
   * @param count - Required non-negative count.
   * @param conditionId - Optional stable condition identifier.
   * @returns A condition with equal lower and upper bounds.
   */
  public forN(count: number, conditionId?: string): Condition {
    return this.range(count, count, conditionId);
  }

  /**
   * Creates a condition requiring at least `count` values.
   * @param count - Inclusive non-negative lower bound.
   * @param conditionId - Optional stable condition identifier.
   * @returns A condition without an upper bound.
   */
  public atLeast(count: number, conditionId?: string): Condition {
    return this.range(count, undefined, conditionId);
  }

  /**
   * Creates a condition consuming at most `count` available values.
   * @param count - Inclusive non-negative upper bound.
   * @param conditionId - Optional stable condition identifier.
   * @returns A condition without a positive lower bound.
   */
  public atMost(count: number, conditionId?: string): Condition {
    return this.range(undefined, count, conditionId);
  }

  /**
   * Creates a bounded condition for queued Channel values.
   * @param atLeast - Optional inclusive lower bound.
   * @param atMost - Optional inclusive upper bound.
   * @param conditionId - Optional stable condition identifier.
   * @returns A validated Channel condition.
   */
  public range(atLeast?: number, atMost?: number, conditionId?: string): Condition {
    validateChannelBounds(atLeast, atMost);
    requireConditionId(conditionId);
    return {
      kind: "channel",
      channelName: this.name,
      ...(atLeast === undefined ? {} : { atLeast }),
      ...(atMost === undefined ? {} : { atMost }),
      ...(conditionId === undefined ? {} : { conditionId }),
    };
  }
}

/**
 * Defines keyed Channel instances that share one typed schema.
 * @typeParam T - Type of every published value.
 */
export class ChannelMap<T> {
  /**
   * Creates a ChannelMap definition.
   * @param name - Non-empty name without `/`, unique within the Flow.
   * @param codec - Element codec shared by every instance.
   */
  public constructor(
    public readonly name: string,
    public readonly codec: Codec<T>,
  ) {
    requirePersistenceDefinitionName(name);
  }

  /**
   * Stages one value for a ChannelMap instance.
   * @param context - Current Step or RPC Context.
   * @param instance - Non-empty logical map key.
   * @param value - Typed value to append.
   */
  public publish(context: Context, instance: string, value: T): void {
    context.publish(this, value, instance);
  }

  /**
   * Stages deletion of one pending message from a ChannelMap instance in an RPC.
   * @param context - Current RPC Context.
   * @param instance - Non-empty ChannelMap instance.
   * @param messageId - Non-empty server-assigned message ID.
   */
  public delete(context: Context, instance: string, messageId: string): void {
    context.deleteChannelMessage(this as ChannelMap<unknown>, messageId, instance);
  }

  /**
   * Returns one instance's queued value count.
   * @param context - Current Step or RPC Context.
   * @param instance - Non-empty logical map key.
   * @returns Non-negative queued value count.
   */
  public size(context: Context, instance: string): number {
    return context.channelSize(this, instance);
  }

  /**
   * Returns the number of non-empty instances visible to the current RPC.
   * @param context - Current RPC Context.
   * @returns The number of keys after including publications buffered by this RPC.
   */
  public getMapSize(context: Context): number {
    return this.getAllInstanceKeys(context).length;
  }

  /**
   * Returns decoded non-empty instance keys in ascending order.
   * @param context - Current RPC Context.
   * @returns Keys including publications buffered by the current RPC.
   */
  public getAllInstanceKeys(context: Context): readonly string[] {
    return context.channelMapKeys(this as ChannelMap<unknown>);
  }

  /**
   * Returns values selected for one instance by the satisfied condition.
   * @param context - Current Step Context.
   * @param instance - Non-empty logical map key.
   * @returns Ordered values for this Step execution.
   */
  public results(context: Context, instance: string): readonly T[] {
    return context.channelResults(this, instance);
  }

  /**
   * Creates an instance condition consuming exactly one value.
   * @param instance - Non-empty logical map key.
   * @param conditionId - Optional stable condition identifier.
   * @returns An exact-one instance condition.
   */
  public forOne(instance: string, conditionId?: string): Condition {
    return this.range(instance, 1, 1, conditionId);
  }

  /**
   * Creates an instance condition consuming exactly `count` values.
   * @param instance - Non-empty logical map key.
   * @param count - Required non-negative count.
   * @param conditionId - Optional stable condition identifier.
   * @returns A condition with equal bounds.
   */
  public forN(instance: string, count: number, conditionId?: string): Condition {
    return this.range(instance, count, count, conditionId);
  }

  /**
   * Creates an instance condition requiring at least `count` values.
   * @param instance - Non-empty logical map key.
   * @param count - Inclusive non-negative lower bound.
   * @param conditionId - Optional stable condition identifier.
   * @returns A condition without an upper bound.
   */
  public atLeast(instance: string, count: number, conditionId?: string): Condition {
    return this.range(instance, count, undefined, conditionId);
  }

  /**
   * Creates an instance condition consuming at most `count` values.
   * @param instance - Non-empty logical map key.
   * @param count - Inclusive non-negative upper bound.
   * @param conditionId - Optional stable condition identifier.
   * @returns A condition without a positive lower bound.
   */
  public atMost(instance: string, count: number, conditionId?: string): Condition {
    return this.range(instance, undefined, count, conditionId);
  }

  /**
   * Creates a bounded condition for one ChannelMap instance.
   * @param instance - Non-empty logical map key.
   * @param atLeast - Optional inclusive lower bound.
   * @param atMost - Optional inclusive upper bound.
   * @param conditionId - Optional stable condition identifier.
   * @returns A validated instance condition.
   */
  public range(
    instance: string,
    atLeast?: number,
    atMost?: number,
    conditionId?: string,
  ): Condition {
    validateChannelBounds(atLeast, atMost);
    requireConditionId(conditionId);
    return {
      kind: "channel",
      channelName: this.name,
      instance,
      ...(atLeast === undefined ? {} : { atLeast }),
      ...(atMost === undefined ? {} : { atMost }),
      ...(conditionId === undefined ? {} : { conditionId }),
    };
  }
}

/**
 * Describes the durable readiness rule evaluated before a Step executes.
 *
 * @example
 * ```ts
 * const approvals = new Channel("approvals", stringCodec);
 * const wait = Wait.anyOf(
 *   approvals.forOne("approved"),
 *   Timer.byDuration(5 * 60_000, "timeout"),
 * );
 * ```
 */
export type Wait = Readonly<{
  /** Combination rule interpreted by Dex. */
  kind: "skipImmediately" | "allOf" | "anyOf" | "anyCombinationOf";
  /** Direct conditions for all-of or any-of waits. */
  conditions: readonly Condition[];
  /** Alternative all-of groups for any-combination waits. */
  combinations: readonly ConditionCombination[];
}>;

/** Creates durable Step Wait values. */
export const Wait = Object.freeze({
  /**
   * Creates an immediate Wait.
   * @returns A Wait that proceeds to `execute` immediately.
   */
  skipImmediately(): Wait {
    return { kind: "skipImmediately", conditions: [], combinations: [] };
  },
  /**
   * Creates a Wait for one condition.
   * @param condition - The only readiness condition.
   * @returns An all-of Wait with the condition.
   */
  until(condition: Condition): Wait {
    return Wait.allOf(condition);
  },
  /**
   * Creates a Wait requiring every condition.
   * @param conditions - Conditions evaluated as one all-of group.
   * @returns An all-of Wait.
   */
  allOf(...conditions: readonly Condition[]): Wait {
    return { kind: "allOf", conditions, combinations: [] };
  },
  /**
   * Creates a Wait that continues when any condition is ready.
   * @param conditions - Alternative readiness conditions.
   * @returns An any-of Wait.
   */
  anyOf(...conditions: readonly Condition[]): Wait {
    return { kind: "anyOf", conditions, combinations: [] };
  },
  /**
   * Creates a Wait accepting any complete condition combination.
   * Every Condition requires a non-empty user ID; the same instance may be reused.
   * @param combinations - Alternative all-of condition groups.
   * @returns An any-combination Wait.
   */
  anyCombinationOf(...combinations: readonly ConditionCombination[]): Wait {
    return { kind: "anyCombinationOf", conditions: [], combinations };
  },
});
