// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { Codec } from "./codec.js";
import type { Context } from "./context.js";
import {
  requireConditionId,
  requireName,
  validateChannelBounds,
} from "./validation.js";

export interface Condition {
  readonly kind: "timer" | "channel";
  readonly conditionId?: string;
  readonly durationMs?: number;
  readonly channelName?: string;
  readonly instance?: string;
  readonly atLeast?: number;
  readonly atMost?: number;
}

export interface ConditionCombination {
  readonly conditions: readonly Condition[];
}

export const ConditionCombination = Object.freeze({
  of(...conditions: readonly Condition[]): ConditionCombination {
    return { conditions };
  },
});

export const Timer = Object.freeze({
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

export class Channel<T> {
  public constructor(
    public readonly name: string,
    public readonly codec: Codec<T>,
  ) {
    requireName(name);
  }

  public publish(context: Context, value: T): void {
    context.publish(this, value);
  }

  public size(context: Context): number {
    return context.channelSize(this as Channel<unknown>);
  }

  public results(context: Context): readonly T[] {
    return context.channelResults(this);
  }

  public forOne(conditionId?: string): Condition {
    return this.range(1, 1, conditionId);
  }

  public forN(count: number, conditionId?: string): Condition {
    return this.range(count, count, conditionId);
  }

  public atLeast(count: number, conditionId?: string): Condition {
    return this.range(count, undefined, conditionId);
  }

  public atMost(count: number, conditionId?: string): Condition {
    return this.range(undefined, count, conditionId);
  }

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

export class ChannelMap<T> {
  public constructor(
    public readonly name: string,
    public readonly codec: Codec<T>,
  ) {
    requireName(name);
  }

  public publish(context: Context, instance: string, value: T): void {
    context.publish(this, value, instance);
  }

  public size(context: Context, instance: string): number {
    return context.channelSize(this, instance);
  }

  public results(context: Context, instance: string): readonly T[] {
    return context.channelResults(this, instance);
  }

  public forOne(instance: string, conditionId?: string): Condition {
    return this.range(instance, 1, 1, conditionId);
  }

  public forN(instance: string, count: number, conditionId?: string): Condition {
    return this.range(instance, count, count, conditionId);
  }

  public atLeast(instance: string, count: number, conditionId?: string): Condition {
    return this.range(instance, count, undefined, conditionId);
  }

  public atMost(instance: string, count: number, conditionId?: string): Condition {
    return this.range(instance, undefined, count, conditionId);
  }

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

export type Wait = Readonly<{
  kind: "skipImmediately" | "allOf" | "anyOf" | "anyCombinationOf";
  conditions: readonly Condition[];
  combinations: readonly ConditionCombination[];
}>;

export const Wait = Object.freeze({
  skipImmediately(): Wait {
    return { kind: "skipImmediately", conditions: [], combinations: [] };
  },
  until(condition: Condition): Wait {
    return Wait.allOf(condition);
  },
  allOf(...conditions: readonly Condition[]): Wait {
    return { kind: "allOf", conditions, combinations: [] };
  },
  anyOf(...conditions: readonly Condition[]): Wait {
    return { kind: "anyOf", conditions, combinations: [] };
  },
  anyCombinationOf(...combinations: readonly ConditionCombination[]): Wait {
    return { kind: "anyCombinationOf", conditions: [], combinations };
  },
});
