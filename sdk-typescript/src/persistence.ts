// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { Channel, ChannelMap } from "./wait.js";
import { markAttributeStoreSynced } from "./attribute-store-sync.js";
import type { Codec } from "./codec.js";
import type { Context } from "./context.js";
import type { Stream } from "./stream.js";
import { requireMapInstance, requirePersistenceDefinitionName } from "./validation.js";

/** Selects AttributeMap entries for an RPC snapshot. */
export interface AttributeMapLoad {
  /** Exact AttributeMap definition registered with the Flow. */
  readonly attributeMap: AttributeMap<unknown>;
  /** One logical instance key; omitted to load every current instance. */
  readonly instance?: string;
}

/** Selects how Dex indexes an Attribute for Flow search. */
export const IndexType = Object.freeze({
  /** Indexes an exact string value. */
  KEYWORD: "keyword",
  /** Indexes a string for tokenized full-text matching. */
  FULL_TEXT: "fullText",
  /** Indexes every string in an array as a keyword. */
  KEYWORD_ARRAY: "keywordArray",
  /** Indexes a signed 64-bit integer. */
  INT: "int",
  /** Indexes a finite floating-point number. */
  DOUBLE: "double",
  /** Indexes a Boolean value. */
  BOOL: "bool",
  /** Indexes a JavaScript Date value. */
  DATETIME: "datetime",
} as const);

/** Represents a value from {@link IndexType}. */
export type IndexType = (typeof IndexType)[keyof typeof IndexType];

/** Configures the search index for an Attribute or AttributeMap. */
export interface AttributeIndex {
  /** Value representation accepted by the search index. */
  readonly type: IndexType;
  /** Physical search key; indexed AttributeMaps require an explicit key. */
  readonly indexKey?: string;
}

/** Describes an Attribute lock acquired for a Step or RPC handler. */
export interface AttributeLock {
  /** Singleton Attribute or AttributeMap definition to lock. */
  readonly attribute: Attribute<unknown> | AttributeMap<unknown>;
  /** AttributeMap instance; omitted for a singleton Attribute. */
  readonly instance?: string;
}

/**
 * Defines a typed, durable singleton value owned by a Flow.
 *
 * @example
 * ```ts
 * const status = new Attribute("status", stringCodec, {
 *   type: IndexType.KEYWORD,
 * });
 * const persistence: PersistenceSchema = { attributes: [status] };
 * ```
 * @typeParam T - Value encoded by the Attribute's codec.
 */
export class Attribute<T> {
  /**
   * Creates an Attribute definition for a PersistenceSchema.
   * @param name - Non-empty logical name without `/`, unique within the Flow.
   * @param codec - Value codec used by handlers and Client calls.
   * @param index - Optional visibility search index.
   */
  public constructor(
    public readonly name: string,
    public readonly codec: Codec<T>,
    public readonly index?: AttributeIndex,
  ) {
    requirePersistenceDefinitionName(name);
  }

  /**
   * Reads the current value from handler decision state.
   * @param context - Current Step or RPC Context.
   * @returns The decoded Attribute value.
   */
  public get(context: Context): T {
    return context.getAttribute(this);
  }

  /**
   * Stages a value to persist with the current decision.
   * @param context - Current Step or RPC Context.
   * @param value - Typed value to persist.
   */
  public set(context: Context, value: T): void {
    context.setAttribute(this, value);
  }

  /**
   * Stages deletion in the current decision.
   * @param context - Current Step or RPC Context.
   */
  public delete(context: Context): void {
    context.deleteAttribute(this as Attribute<unknown>);
  }

  /**
   * Returns an immutable Attribute definition whose writes are projected to the Flow's Attribute Store.
   *
   * Projection is asynchronous and latest-state only. Deletion writes SQL `NULL`, projection failures do
   * not roll back Flow Attribute writes, and the Flow must select a configured Attribute Store name.
   * @returns A new synced definition; this definition remains unchanged.
   */
  public syncToAttributeStore(): Attribute<T> {
    return markAttributeStoreSynced(new Attribute(this.name, this.codec, this.index));
  }

  /**
   * Creates a lock request for this Attribute.
   * @returns A lock for this singleton Attribute.
   */
  public lock(): AttributeLock {
    return { attribute: this as Attribute<unknown> };
  }
}

/**
 * Defines a typed family of durable values keyed by instance.
 * Slash is prohibited in instance keys because it is a reserved character.
 * @typeParam T - Value encoded for every map instance.
 */
export class AttributeMap<T> {
  /**
   * Creates an AttributeMap definition for a PersistenceSchema.
   * @param name - Non-empty logical name without `/`, unique within the Flow.
   * @param codec - Value codec shared by every instance.
   * @param index - Optional shared visibility search index.
   */
  public constructor(
    public readonly name: string,
    public readonly codec: Codec<T>,
    public readonly index?: AttributeIndex,
  ) {
    requirePersistenceDefinitionName(name);
  }

  /**
   * Reads one map instance from handler decision state.
   * @param context - Current Step or RPC Context.
   * @param instance - The map instance. Slash is prohibited because it is a reserved character.
   * @returns The decoded instance value.
   */
  public get(context: Context, instance: string): T {
    return context.getAttribute(this, instance);
  }

  /**
   * Stages one map-instance write.
   * @param context - Current Step or RPC Context.
   * @param instance - The map instance. Slash is prohibited because it is a reserved character.
   * @param value - Typed value to persist.
   */
  public set(context: Context, instance: string, value: T): void {
    context.setAttribute(this, value, instance);
  }

  /**
   * Stages deletion of one map instance.
   * @param context - Current Step or RPC Context.
   * @param instance - The map instance. Slash is prohibited because it is a reserved character.
   */
  public delete(context: Context, instance: string): void {
    context.deleteAttribute(this as AttributeMap<unknown>, instance);
  }

  /**
   * Selects one logical instance for an RPC snapshot.
   * @param instance - Non-empty logical map key. The SDK escapes it for the protocol.
   * @returns A typed exact-instance selection for {@link RPCOptions.loadAttributeMaps}.
   */
  public load(instance: string): AttributeMapLoad {
    requireName(instance);
    return { attributeMap: this as AttributeMap<unknown>, instance };
  }

  /**
   * Selects every current instance for an RPC snapshot.
   * Loading does not lock the map or isolate it from concurrent writers.
   * @returns A typed all-instances selection for {@link RPCOptions.loadAttributeMaps}.
   */
  public loadAll(): AttributeMapLoad {
    return { attributeMap: this as AttributeMap<unknown> };
  }

  /**
   * Returns the number of existing instances, including buffered writes.
   * @param context - Current Step or RPC Context.
   * @returns The number of keys visible after decision-local writes and deletions.
   */
  public getMapSize(context: Context): number {
    return this.getAllInstanceKeys(context).length;
  }

  /**
   * Returns decoded existing instance keys in ascending order.
   * @param context - Current Step or RPC Context.
   * @returns Keys visible after decision-local writes and deletions.
   */
  public getAllInstanceKeys(context: Context): readonly string[] {
    return context.attributeMapKeys(this as AttributeMap<unknown>);
  }

  /**
   * Returns an immutable AttributeMap definition whose writes are projected to the Flow's Attribute Store.
   *
   * Projection is asynchronous and latest-state only. Each map instance uses its physical Attribute name.
   * Deletion writes SQL `NULL`, projection failures do not roll back Flow Attribute writes, and the Flow
   * must select a configured Attribute Store name.
   * @returns A new synced definition; this definition remains unchanged.
   */
  public syncToAttributeStore(): AttributeMap<T> {
    return markAttributeStoreSynced(new AttributeMap(this.name, this.codec, this.index));
  }

  /**
   * Creates a lock request scoped to one map instance.
   * @param instance - The map instance. Slash is prohibited because it is a reserved character.
   * @returns A lock for the requested instance.
   */
  public lock(instance: string): AttributeLock {
    requireMapInstance(instance);
    return { attribute: this as AttributeMap<unknown>, instance };
  }
}

/** Declares the Attributes, Channels, and Streams owned by a Flow type. */
export interface PersistenceSchema {
  /** Singleton and map Attribute definitions with unique names. */
  readonly attributes?: readonly (Attribute<unknown> | AttributeMap<unknown>)[];
  /** Singleton and map Channel definitions with unique names. */
  readonly channels?: readonly (Channel<unknown> | ChannelMap<unknown>)[];
  /** Best-effort Stream definitions with unique names. */
  readonly streams?: readonly Stream<unknown>[];
}
