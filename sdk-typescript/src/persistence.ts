// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { Channel, ChannelMap } from "./wait.js";
import type { Codec } from "./codec.js";
import type { Context } from "./context.js";
import { requireName } from "./validation.js";

export const IndexType = Object.freeze({
  KEYWORD: "keyword",
  FULL_TEXT: "fullText",
  KEYWORD_ARRAY: "keywordArray",
  INT: "int",
  DOUBLE: "double",
  BOOL: "bool",
  DATETIME: "datetime",
} as const);

export type IndexType = (typeof IndexType)[keyof typeof IndexType];

export interface AttributeIndex {
  readonly type: IndexType;
  readonly indexKey?: string;
}

export interface AttributeLock {
  readonly attribute: Attribute<unknown> | AttributeMap<unknown>;
  readonly instance?: string;
}

export class Attribute<T> {
  public constructor(
    public readonly name: string,
    public readonly codec: Codec<T>,
    public readonly index?: AttributeIndex,
  ) {
    requireName(name);
  }

  public get(context: Context): T {
    return context.getAttribute(this);
  }

  public set(context: Context, value: T): void {
    context.setAttribute(this, value);
  }

  public delete(context: Context): void {
    context.deleteAttribute(this as Attribute<unknown>);
  }

  public lock(): AttributeLock {
    return { attribute: this as Attribute<unknown> };
  }
}

export class AttributeMap<T> {
  public constructor(
    public readonly name: string,
    public readonly codec: Codec<T>,
    public readonly index?: AttributeIndex,
  ) {
    requireName(name);
  }

  public get(context: Context, instance: string): T {
    return context.getAttribute(this, instance);
  }

  public set(context: Context, instance: string, value: T): void {
    context.setAttribute(this, value, instance);
  }

  public delete(context: Context, instance: string): void {
    context.deleteAttribute(this as AttributeMap<unknown>, instance);
  }

  public lock(instance: string): AttributeLock {
    requireName(instance);
    return { attribute: this as AttributeMap<unknown>, instance };
  }
}

export interface PersistenceSchema {
  readonly attributes?: readonly (Attribute<unknown> | AttributeMap<unknown>)[];
  readonly channels?: readonly (Channel<unknown> | ChannelMap<unknown>)[];
}
