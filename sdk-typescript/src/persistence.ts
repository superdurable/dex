// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Join } from "./join.js";
import type { Channel, ChannelMap } from "./wait.js";
import {
  isHardDeleteOnNullDefinition,
  markHardDeleteOnNull,
  requireHardDeleteOnNullCompatible,
  requireNotHardDeleteOnNull,
} from "./attribute-hard-delete.js";
import {
  carryLockRequirement,
  markLockRequirement,
  ownKeyLockRequirement,
} from "./attribute-lock-requirement.js";
import { carryPlaintextAttribute, markPlaintextAttribute } from "./attribute-plaintext.js";
import { isAttributeStoreSynced, markAttributeStoreSynced } from "./attribute-store-sync.js";
import type { Codec } from "./codec.js";
import type { Context } from "./context.js";
import type { Stream } from "./stream.js";
import { requireMapInstance, requirePersistenceDefinitionName } from "./validation.js";

/** Selects one AttributeMap instance for a handler snapshot. */
export interface AttributeMapLoad {
  /** Exact AttributeMap definition registered with the Flow. */
  readonly attributeMap: AttributeMap<unknown>;
  /** One slash-free logical instance key. */
  readonly instance: string;
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

/**
 * Configures the search index for an Attribute or AttributeMap.
 *
 * Keys share a namespace-wide typed pool across Flow types. Use explicit generic numbered keys,
 * such as keyword1 or int1, and constrain searches by FlowType when a key has different meanings.
 * Two logical indexed fields in one Flow need different keys. Reuse a key only with the same type.
 * A singleton defaults to its Attribute name; an indexed AttributeMap requires an explicit key.
 * Map writes replace that single search field; instances are not combined or individually searchable.
 * Use a singleton KEYWORD_ARRAY Attribute to index a collection instead.
 * Renaming a key does not backfill existing Flows. Do not index secrets or personal information.
 *
 * SDK v1.30.0 recognizes the index capacity and Keyword length sub-statuses that servers can return.
 * With PostgreSQL search, the shared registry allows 10 Keyword keys and 3 of each other type.
 * Registrations are permanent; exceeding capacity fails Worker startup with FAILED_PRECONDITION
 * and ATTRIBUTE_INDEX_CAPACITY_EXCEEDED. Existing keys can be registered again.
 * Keyword values and individual KEYWORD_ARRAY elements are limited to 1,024 UTF-8 bytes.
 * An oversized Client start or RPC write fails with INVALID_ARGUMENT and INDEXED_KEYWORD_TOO_LONG;
 * the request commits nothing. An oversized Step result, including SubFlow initial Attributes,
 * fails the attempt without retry. Use TEXT or a short derived key for longer strings.
 * Builtin and ParadeDB search do not impose those fixed capacity or Keyword limits.
 *
 * @example
 * ```ts
 * const status = new Attribute("order-status", stringCodec, {
 *   type: IndexType.KEYWORD,
 *   indexKey: "keyword1",
 * });
 * const persistence: PersistenceSchema = { attributes: [status] };
 * ```
 */
export interface AttributeIndex {
  /** Value representation accepted by the search index. */
  readonly type: IndexType;
  /** Physical search key; indexed AttributeMaps require an explicit key. */
  readonly indexKey?: string;
}

/**
 * Describes an Attribute lock acquired for a Step or RPC handler.
 * A lock coordinates only handlers that request the same Attribute or map-instance lock.
 * For read-modify-write on an AttributeMap instance, select both its load and its lock.
 * Step locks survive retries and backoff until the logical method ends; RPCs report conflicts immediately.
 * A lock does not lock a Channel snapshot; every cooperating writer must use the same lock.
 */
export interface AttributeLock {
  /** Singleton Attribute or AttributeMap definition to lock. */
  readonly attribute: Attribute<unknown> | AttributeMap<unknown>;
  /** AttributeMap instance; omitted for a singleton Attribute. */
  readonly instance?: string;
}

/**
 * Defines a typed, durable singleton value owned by a Flow.
 * Register the exact definition in getPersistenceSchema and reuse it for handler and Client operations.
 * Handler writes are staged with the result and reads see their own staged writes.
 * Absent handler reads use the codec's zero value: false, 0, or 0n for scalar numeric/Boolean
 * codecs and undefined for other codecs. Client.getAttributes instead returns undefined for absent values.
 * Other callers see committed values. A failed handler does not commit ordinary Attribute writes.
 * Setting null or undefined deletes the value; hard-delete definitions have additional rules below.
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
   *
   * The read includes writes staged earlier in the same handler. Reading an Attribute that has no
   * value does not throw. It returns `false` for a codec with wire kind `bool`, `0n` for `int64`,
   * `0` for `double`, and `undefined` for every other codec, including `stringCodec`. The return
   * type is still `T`, so check for `undefined` before using a string or JSON value. A value that
   * was deleted reads the same way.
   *
   * A numeric or Boolean read therefore cannot tell an unset value from a stored zero or `false`.
   * When that difference matters, use a string or JSON codec, or keep a separate Attribute that
   * records whether the value was set.
   *
   * @example
   * ```ts
   * const reviewStatus = new Attribute("review-status", stringCodec);
   *
   * class OrderFlow implements Flow<void> {
   *   public getSteps() {
   *     return StepList.empty();
   *   }
   *
   *   public getPersistenceSchema(): PersistenceSchema {
   *     return { attributes: [reviewStatus] };
   *   }
   *
   *   @rpc({ outputCodec: stringCodec })
   *   public getReviewStatus(context: Context): RPCResult<string> {
   *     const status: string | undefined = reviewStatus.get(context);
   *     return { output: status ?? "pending" };
   *   }
   * }
   * ```
   * @param context - Current Step or RPC Context.
   * @returns The decoded Attribute value. When no value is stored, `false`, `0n`, or `0` for a
   *   `bool`, `int64`, or `double` codec, and `undefined` for any other codec.
   * @throws {TypeError} When this definition is not registered in the current Flow's
   *   PersistenceSchema.
   * @throws {ValueMappingError} When the codec cannot decode the stored value.
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
   * @throws {TypeError} When this definition uses {@link Attribute.hardDeleteOnNull}.
   */
  public syncToAttributeStore(): Attribute<T> {
    requireNotHardDeleteOnNull(this, "syncToAttributeStore");
    return markAttributeStoreSynced(copyAttributeDefinitionOptions(
      this,
      new Attribute(this.name, this.codec, this.index),
    ));
  }

  /**
   * Returns an immutable Attribute definition whose values the SDK never encrypts.
   *
   * Use it when a Client or Worker has a `payloadCodec` and the Dex Server must read this value.
   * `Client.waitForAttributeMatch` and Action conditions compare the stored value on the Server,
   * so they accept only an indexed or Plaintext Attribute. External readers of an Attribute Store
   * column also see the value in clear. Indexed Attributes are never encrypted, so they need no
   * declaration.
   *
   * The declaration is local to the SDK and is never sent to the Server. Register the returned
   * definition in the Flow's PersistenceSchema. The option keeps the index, Attribute Store
   * synchronization, and hard-delete options of this definition. Without a `payloadCodec`, it has
   * no effect.
   *
   * @example
   * ```ts
   * const caseStatus = new Attribute("case-status", stringCodec).plaintext();
   * const persistence: PersistenceSchema = { attributes: [caseStatus] };
   * ```
   * @returns A new Plaintext definition; this definition remains unchanged.
   * @since 1.45.16
   */
  public plaintext(): Attribute<T> {
    return markPlaintextAttribute(copyAttributeDefinitionOptions(
      this,
      new Attribute(this.name, this.codec, this.index),
    ));
  }

  /**
   * Returns an immutable Attribute definition for short-lived secrets that Dex can permanently delete.
   *
   * Use this option for values such as API keys that must not remain in Flow history. Dex stores
   * each value of a hard-delete Attribute only in the Dex blob store, never in Flow history.
   * Deleting the Attribute or setting it to `null` or `undefined` permanently deletes the stored value.
   * Register the returned definition in the Flow's PersistenceSchema. Use that same definition for
   * handler reads and writes, InitialAttribute values, and SubFlow initial Attributes.
   *
   * Only string and object values are supported. Object values include JSON values and byte arrays.
   * Numeric and Boolean values are rejected. A built-in numeric or Boolean codec fails here. A custom
   * codec that encodes a number or Boolean fails when the value is written. Deletion is always allowed.
   * A hard-delete Attribute cannot have an index and cannot use {@link Attribute.syncToAttributeStore}.
   * {@link Client.waitForAttributeMatch} rejects a hard-delete Attribute before contacting Dex.
   *
   * Writes require a Dex Server with an enabled blob store and Server protocol version 3 or later.
   * Otherwise, the write fails. The Client checks the Server protocol version once before its first
   * request of any method and requires version 4. The Worker checks it at startup and fails any handler result that writes a
   * hard-delete Attribute to an older Server. Step and RPC handlers receive the value inline. The SDK
   * never stores it in the local blob cache.
   *
   * Limitations:
   * - Each Flow keeps one stored copy per Attribute key and overwrites it in place.
   * - A Step or RPC attempt writes or deletes the stored value before Dex commits its result.
   *   A retried attempt, other Steps and RPCs, and Client reads can therefore observe a write or
   *   deletion from an attempt that later failed.
   * - When concurrent Steps or RPCs write the same key, the final stored value may not be the value
   *   of the last committed write.
   * - A failed `startFlow` or SubFlow start for a Flow ID that is already running still overwrites
   *   the running Flow's stored value. A null initial value deletes it.
   * - Executions that reuse a Flow ID share the stored value.
   * - {@link Client.timeTravel} does not restore a deleted value. A deleted value reads as absent.
   * - A restarted SubFlow starts from the original initial value. If an
   *   earlier run deleted that value, the new run reads it as absent.
   * - Removing {@link Attribute.hardDeleteOnNull} from a definition leaves the stored value in place
   *   until Dex removes the Flow. Delete the value with the hard-delete definition first.
   * - Values remain until they are deleted, or until Dex removes the Flow through retention and
   *   blob cleanup.
   * - Blob store backups or bucket versioning can retain copies outside Dex's control.
   *
   * @example
   * ```ts
   * const apiKey = new Attribute("api-key", stringCodec).hardDeleteOnNull();
   *
   * class CallVendorStep implements Step<string> {
   *   public readonly inputCodec = stringCodec;
   *
   *   public getStepType(): string {
   *     return "CallVendorStep";
   *   }
   *
   *   public async execute(context: Context, vendorUrl: string): Promise<StepDecision> {
   *     const response = await fetch(vendorUrl, {
   *       headers: { authorization: `Bearer ${apiKey.get(context)}` },
   *     });
   *     apiKey.delete(context);
   *     return gracefulComplete(response.status);
   *   }
   * }
   *
   * class VendorCallFlow implements Flow<string> {
   *   private readonly callVendor = new CallVendorStep();
   *
   *   public getFlowType(): string {
   *     return "VendorCallFlow";
   *   }
   *
   *   public getSteps() {
   *     return StepList.startStep(this.callVendor);
   *   }
   *
   *   public getPersistenceSchema(): PersistenceSchema {
   *     return { attributes: [apiKey] };
   *   }
   * }
   *
   * const flow = new VendorCallFlow();
   * const client = new Client(new Registry([flow]), cache);
   * await client.startFlow(flow, "vendor-call-42", "https://vendor.example/report", {
   *   attributes: [InitialAttribute.of(apiKey, process.env.VENDOR_API_KEY ?? "")],
   * });
   * ```
   * @returns A new hard-delete definition; this definition remains unchanged.
   * @throws {TypeError} When this definition has an index, uses Attribute Store synchronization, or
   * uses a built-in numeric or Boolean codec.
   */
  public hardDeleteOnNull(): Attribute<T> {
    requireHardDeleteOnNullCompatible(this as Attribute<unknown>);
    return markHardDeleteOnNull(copyAttributeDefinitionOptions(
      this,
      new Attribute(this.name, this.codec, this.index),
    ));
  }

  /**
   * Returns an immutable Attribute definition whose every write must hold this Attribute's own lock.
   *
   * Use it for a value that handlers read and then write, such as a counter or a total, so that a
   * handler that forgets the lock fails instead of losing a concurrent update. The declaration is
   * opt-in. Without it, any handler can write the Attribute without a lock.
   *
   * The Worker enforces the requirement. After a Step's `waitFor` or `execute`, or an RPC, returns,
   * the Worker compares each {@link Attribute.set} and {@link Attribute.delete} of this Attribute
   * with the locks that the handler registered. When the lock is missing, the invocation fails with
   * {@link MissingAttributeLockError}, and Dex discards every change buffered for its result.
   *
   * Only registered locks count: `waitForLockAttributes` or `executeLockAttributes` of the Step's
   * own {@link StepOptions}, and `lockAttributes` of the RPC's {@link RPCOptions}. Locks chosen for
   * one call do not count. These are `lockAttributeMapInstances` of {@link RPCInvokeOptions} and the
   * options of one {@link StepMovement}. A Flow timeout handler is not checked, because its locks
   * come from the Flow start options. Attribute values set when a Flow starts are not checked
   * either.
   *
   * The declaration is local to the SDK and is never sent to the Server. Register the returned
   * definition in the Flow's PersistenceSchema, and build every Worker from the same definition.
   * The returned definition keeps the index, Attribute Store synchronization, hard-delete, and
   * Plaintext options of this definition. It replaces an earlier lock requirement.
   *
   * The Worker compares writes with the registered locks, while Dex acquires the locks each call
   * sends. A movement's {@link StepOptions} replace the target Step's options, including its lock
   * fields, so keep the required lock in such options.
   *
   * @example
   * ```ts
   * const orderTotal = new Attribute("order-total", doubleCodec).writesRequireLock();
   *
   * class AddItemStep implements Step<number> {
   *   public readonly inputCodec = doubleCodec;
   *
   *   public getStepType(): string {
   *     return "AddItemStep";
   *   }
   *
   *   public getStepOptions(): StepOptions {
   *     return { executeLockAttributes: [orderTotal.lock()] };
   *   }
   *
   *   public execute(context: Context, price: number): StepDecision {
   *     orderTotal.set(context, orderTotal.get(context) + price);
   *     return gracefulComplete(orderTotal.get(context));
   *   }
   * }
   *
   * class OrderFlow implements Flow<number> {
   *   private readonly addItem = new AddItemStep();
   *
   *   public getFlowType(): string {
   *     return "OrderFlow";
   *   }
   *
   *   public getSteps() {
   *     return StepList.startStep(this.addItem);
   *   }
   *
   *   public getPersistenceSchema(): PersistenceSchema {
   *     return { attributes: [orderTotal] };
   *   }
   * }
   * ```
   * @returns A new definition that requires its own lock for every write; this definition remains
   *   unchanged.
   * @since 1.45.17
   */
  public writesRequireLock(): Attribute<T> {
    return markLockRequirement(copyAttributeDefinitionOptions(
      this,
      new Attribute(this.name, this.codec, this.index),
    ), ownKeyLockRequirement);
  }

  /**
   * Returns an immutable Attribute definition whose every write must hold another Attribute's lock.
   *
   * Use it when one coordination Attribute protects an invariant across several values. Every
   * handler that writes any of them then registers the same coordination lock and runs one at a
   * time within the Flow run. The lock Attribute only names the lock; its value is not used.
   *
   * Enforcement is the same as for {@link Attribute.writesRequireLock}: a `waitFor`, `execute`,
   * or RPC handler that writes this Attribute without registering `lockAttribute.lock()` fails
   * with {@link MissingAttributeLockError}, and Dex discards every change buffered for its result.
   * Only locks registered in the handler's {@link StepOptions} or {@link RPCOptions} count.
   *
   * Register `lockAttribute` in the same Flow's PersistenceSchema, as the same definition object.
   * Registry construction throws {@link FlowDefinitionError} when it is missing, when it is a
   * different object with the same name, or when that name belongs to an AttributeMap. The
   * returned definition keeps the other options of this definition. It replaces an earlier lock
   * requirement.
   *
   * @example
   * ```ts
   * const cartLock = new Attribute("cart-lock", booleanCodec);
   * const cartTotal = new Attribute("cart-total", doubleCodec).writesRequireAttributeLock(cartLock);
   *
   * class CartFlow implements Flow<void> {
   *   public getFlowType(): string {
   *     return "CartFlow";
   *   }
   *
   *   public getSteps() {
   *     return StepList.empty();
   *   }
   *
   *   public getPersistenceSchema(): PersistenceSchema {
   *     return { attributes: [cartLock, cartTotal] };
   *   }
   *
   *   @rpc({ inputCodec: doubleCodec, lockAttributes: [cartLock.lock()] })
   *   public addToTotal(context: Context, price: number): void {
   *     cartTotal.set(context, cartTotal.get(context) + price);
   *   }
   * }
   * ```
   * @param lockAttribute - Singleton Attribute whose lock every write requires.
   * @returns A new definition that requires `lockAttribute`'s lock for every write; this definition
   *   remains unchanged.
   * @throws {TypeError} When `lockAttribute` is not a singleton {@link Attribute}.
   * @since 1.45.17
   */
  public writesRequireAttributeLock(lockAttribute: Attribute<unknown>): Attribute<T> {
    const requiredLockAttribute = requireSingletonLockAttribute(lockAttribute);
    return markLockRequirement(copyAttributeDefinitionOptions(
      this,
      new Attribute(this.name, this.codec, this.index),
    ), { kind: "attribute", lockAttribute: requiredLockAttribute });
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
 * Each nonempty, slash-free instance key has an independent value and lock.
 * Select map instances or the whole map in Step or RPC load options before reading.
 * Reading unselected state throws AttributeMapNotLoadedError; see StepOptions and RPCOptions.
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
   *
   * The handler must load the instance, or the whole map, through its load options. Reading an
   * instance outside that scope throws `AttributeMapNotLoadedError`, whether or not the
   * instance has a value.
   *
   * Reading a loaded instance that has no value does not throw. Like {@link Attribute.get}, it
   * returns `false`, `0n`, or `0` for a `bool`, `int64`, or `double` codec, and `undefined` for
   * every other codec.
   *
   * A read-modify-write also locks the instance, so concurrent handlers do not overwrite each
   * other's changes.
   *
   * @example
   * ```ts
   * interface Note {
   *   readonly lines: readonly string[];
   * }
   *
   * const notes = new AttributeMap("notes", jsonCodec<Note>());
   *
   * class NotebookFlow implements Flow<void> {
   *   public getSteps() {
   *     return StepList.empty();
   *   }
   *
   *   public getPersistenceSchema(): PersistenceSchema {
   *     return { attributes: [notes] };
   *   }
   *
   *   @rpc({
   *     inputCodec: stringCodec,
   *     lockAttributes: [notes.lock("n1")],
   *     loadAttributeMapInstances: [notes.load("n1")],
   *   })
   *   public appendNote(context: Context, text: string): void {
   *     const note: Note = notes.get(context, "n1") ?? { lines: [] };
   *     notes.set(context, "n1", { lines: [...note.lines, text] });
   *   }
   * }
   * ```
   * @param context - Current Step or RPC Context.
   * @param instance - The map instance. It must not be empty or only whitespace, and it must not
   *   contain `/` because slash is a reserved character.
   * @returns The decoded instance value. When the loaded instance has no value, `false`, `0n`, or
   *   `0` for a `bool`, `int64`, or `double` codec, and `undefined` for any other codec.
   * @throws {AttributeMapNotLoadedError} When the handler did not load this instance or the whole
   *   map.
   * @throws {TypeError} When `instance` is empty, contains only whitespace, or contains `/`, or
   *   when this definition is not registered in the current Flow's PersistenceSchema.
   * @throws {ValueMappingError} When the codec cannot decode the stored value.
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
   * Selects one logical instance for a handler snapshot.
   * @param instance - Non-empty logical map key. The SDK escapes it for the protocol.
   * @returns An exact instance load for RPC, Step, or timeout-handler options.
   */
  public load(instance: string): AttributeMapLoad {
    requireMapInstance(instance);
    return { attributeMap: this as AttributeMap<unknown>, instance };
  }

  /**
   * Selects one instance of this AttributeMap for {@link Client.getAttributes}.
   *
   * Use the result as one read in a `getAttributes` call. The selection keeps this AttributeMap's
   * value type, so the matching result element has type `T | undefined`. To load an instance into a
   * Step, RPC, or timeout-handler snapshot, use {@link AttributeMap.load}.
   *
   * @example
   * ```ts
   * type Note = { readonly text: string };
   * const notes = new AttributeMap("notes", jsonCodec<Note>());
   * const [note] = await client.getAttributes("order-42", notes.instance("n1"));
   * const text = note?.text ?? "no note yet";
   * ```
   * @param instance - Non-empty logical map key. Slash is prohibited because it is a reserved
   * character. The SDK escapes the key for the protocol.
   * @returns A new selection of this AttributeMap and `instance`. It does not contact Dex.
   * @throws {TypeError} When `instance` is empty, contains only whitespace, or contains `/`.
   * @since 1.29.0
   */
  public instance(instance: string): AttributeMapInstance<T> {
    return new AttributeMapInstance(this, instance);
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
   * @throws {TypeError} When this definition uses {@link AttributeMap.hardDeleteOnNull}.
   */
  public syncToAttributeStore(): AttributeMap<T> {
    requireNotHardDeleteOnNull(this, "syncToAttributeStore");
    return markAttributeStoreSynced(copyAttributeDefinitionOptions(
      this,
      new AttributeMap(this.name, this.codec, this.index),
    ));
  }

  /**
   * Returns an immutable AttributeMap definition whose instance values the SDK never encrypts.
   *
   * Use it when a Client or Worker has a `payloadCodec` and the Dex Server must read these values,
   * for example for `Client.waitForAttributeMatch` on an instance. Indexed AttributeMaps are never
   * encrypted, so they need no declaration. The declaration is local to the SDK and is never sent
   * to the Server. Register the returned definition in the Flow's PersistenceSchema. The option
   * keeps the index, Attribute Store synchronization, and hard-delete options of this definition.
   * Without a `payloadCodec`, it has no effect.
   *
   * @example
   * ```ts
   * const reviewStates = new AttributeMap("review-states", stringCodec).plaintext();
   * const persistence: PersistenceSchema = { attributes: [reviewStates] };
   * ```
   * @returns A new Plaintext definition; this definition remains unchanged.
   * @since 1.45.16
   */
  public plaintext(): AttributeMap<T> {
    return markPlaintextAttribute(copyAttributeDefinitionOptions(
      this,
      new AttributeMap(this.name, this.codec, this.index),
    ));
  }

  /**
   * Returns an immutable AttributeMap definition for short-lived secrets that Dex can permanently delete.
   *
   * Use this option for per-instance values such as API keys or access tokens that must not remain
   * in Flow history. Dex stores each instance value only in the Dex blob store, never in Flow history.
   * Deleting an instance or setting it to `null` or `undefined` permanently deletes its stored value.
   * Register the returned definition in the Flow's PersistenceSchema. Use that same definition for
   * handler reads and writes, InitialAttribute values, and SubFlow initial Attributes.
   *
   * Only string and object values are supported. Object values include JSON values and byte arrays.
   * Numeric and Boolean values are rejected. A built-in numeric or Boolean codec fails here. A custom
   * codec that encodes a number or Boolean fails when the value is written. Deletion is always allowed.
   * A hard-delete AttributeMap cannot have an index and cannot use
   * {@link AttributeMap.syncToAttributeStore}. {@link Client.waitForAttributeMatch} rejects a
   * hard-delete AttributeMap before contacting Dex.
   *
   * Writes require a Dex Server with an enabled blob store and Server protocol version 3 or later.
   * Otherwise, the write fails. The Client checks the Server protocol version once before its first
   * request of any method and requires version 4. The Worker checks it at startup and fails any handler result that writes a
   * hard-delete AttributeMap instance to an older Server. Step and RPC handlers receive instance
   * values inline. The SDK never stores them in the local blob cache.
   *
   * Limitations:
   * - Each Flow keeps one stored copy per map instance and overwrites it in place.
   * - A Step or RPC attempt writes or deletes the stored value before Dex commits its result.
   *   A retried attempt, other Steps and RPCs, and Client reads can therefore observe a write or
   *   deletion from an attempt that later failed.
   * - When concurrent Steps or RPCs write the same instance, the final stored value may not be the
   *   value of the last committed write.
   * - A failed `startFlow` or SubFlow start for a Flow ID that is already running still overwrites
   *   the running Flow's stored value. A null initial value deletes it.
   * - Executions that reuse a Flow ID share the stored values.
   * - {@link Client.timeTravel} does not restore a deleted value. A deleted instance reads as absent.
   * - A restarted SubFlow starts from the original initial value. If an
   *   earlier run deleted that value, the new run reads it as absent.
   * - Removing {@link AttributeMap.hardDeleteOnNull} from a definition leaves the stored value in place
   *   until Dex removes the Flow. Delete the value with the hard-delete definition first.
   * - Values remain until they are deleted, or until Dex removes the Flow through retention and
   *   blob cleanup.
   * - Blob store backups or bucket versioning can retain copies outside Dex's control.
   *
   * @example
   * ```ts
   * type VendorToken = { readonly token: string };
   * const vendorTokens = new AttributeMap("vendor-tokens", jsonCodec<VendorToken>())
   *   .hardDeleteOnNull();
   *
   * class RevokeTokenStep implements Step<string> {
   *   public readonly inputCodec = stringCodec;
   *
   *   public getStepType(): string {
   *     return "RevokeTokenStep";
   *   }
   *
   *   public getStepOptions(): StepOptions {
   *     return { executeLoadAttributeMapInstances: [vendorTokens.load("billing")] };
   *   }
   *
   *   public async execute(context: Context, revokeUrl: string): Promise<StepDecision> {
   *     const { token } = vendorTokens.get(context, "billing");
   *     await fetch(revokeUrl, { method: "POST", body: token });
   *     vendorTokens.delete(context, "billing");
   *     return gracefulComplete("revoked");
   *   }
   * }
   *
   * class TokenRevocationFlow implements Flow<string> {
   *   private readonly revokeToken = new RevokeTokenStep();
   *
   *   public getFlowType(): string {
   *     return "TokenRevocationFlow";
   *   }
   *
   *   public getSteps() {
   *     return StepList.startStep(this.revokeToken);
   *   }
   *
   *   public getPersistenceSchema(): PersistenceSchema {
   *     return { attributes: [vendorTokens] };
   *   }
   * }
   *
   * const flow = new TokenRevocationFlow();
   * const client = new Client(new Registry([flow]), cache);
   * await client.startFlow(flow, "revoke-42", "https://vendor.example/revoke", {
   *   attributes: [InitialAttribute.mapValue(vendorTokens, "billing", { token: "token-value" })],
   * });
   * ```
   * @returns A new hard-delete definition; this definition remains unchanged.
   * @throws {TypeError} When this definition has an index, uses Attribute Store synchronization, or
   * uses a built-in numeric or Boolean codec.
   */
  public hardDeleteOnNull(): AttributeMap<T> {
    requireHardDeleteOnNullCompatible(this as AttributeMap<unknown>);
    return markHardDeleteOnNull(copyAttributeDefinitionOptions(
      this,
      new AttributeMap(this.name, this.codec, this.index),
    ));
  }

  /**
   * Returns an immutable AttributeMap definition whose instance writes hold their own locks.
   *
   * Use it when handlers read and then write single instances, such as per-item quantities. A write
   * of instance `"a"` then requires `lock("a")` among the handler's registered locks. Registered
   * locks are fixed, so this suits a map with a known set of instances. For instances chosen at run
   * time, use {@link AttributeMap.writesRequireAttributeLock} with a coordination Attribute instead.
   *
   * The Worker enforces the requirement. After a Step's `waitFor` or `execute`, or an RPC, returns,
   * the Worker compares each {@link AttributeMap.set} and {@link AttributeMap.delete} with the locks
   * that the handler registered. When an instance's lock is missing, the invocation fails with
   * {@link MissingAttributeLockError}, and Dex discards every change buffered for its result.
   *
   * Only locks registered in the Step's own {@link StepOptions} or the RPC's {@link RPCOptions}
   * count. `lockAttributeMapInstances` of {@link RPCInvokeOptions} and the options of one
   * {@link StepMovement} do not count. Flow timeout handlers and Attribute values set when a Flow
   * starts are not checked. The declaration is local to the SDK and is never sent to the Server.
   * The returned definition keeps the other options of this definition. It replaces an earlier
   * lock requirement.
   *
   * @example
   * ```ts
   * const quantities = new AttributeMap("quantities", doubleCodec).writesRequireLock();
   *
   * class InventoryFlow implements Flow<void> {
   *   public getFlowType(): string {
   *     return "InventoryFlow";
   *   }
   *
   *   public getSteps() {
   *     return StepList.empty();
   *   }
   *
   *   public getPersistenceSchema(): PersistenceSchema {
   *     return { attributes: [quantities] };
   *   }
   *
   *   @rpc({
   *     lockAttributes: [quantities.lock("apples")],
   *     loadAttributeMapInstances: [quantities.load("apples")],
   *   })
   *   public addApple(context: Context): void {
   *     quantities.set(context, "apples", quantities.get(context, "apples") + 1);
   *   }
   * }
   * ```
   * @returns A new definition that requires each written instance's own lock; this definition
   *   remains unchanged.
   * @since 1.45.17
   */
  public writesRequireLock(): AttributeMap<T> {
    return markLockRequirement(copyAttributeDefinitionOptions(
      this,
      new AttributeMap(this.name, this.codec, this.index),
    ), ownKeyLockRequirement);
  }

  /**
   * Returns an immutable AttributeMap definition whose instance writes hold one Attribute's lock.
   *
   * Use it to protect an invariant across the whole map, or when handlers write instances chosen
   * at run time. Every writer registers the same coordination lock, so all writers of the map run
   * one at a time within the Flow run. The lock Attribute only names the lock; its value is not
   * used.
   *
   * Enforcement is the same as for {@link AttributeMap.writesRequireLock}, except that every
   * instance requires `lockAttribute.lock()`. An instance's own lock does not satisfy the
   * requirement.
   *
   * Register `lockAttribute` in the same Flow's PersistenceSchema, as the same definition object.
   * Registry construction throws {@link FlowDefinitionError} when it is missing, when it is a
   * different object with the same name, or when that name belongs to an AttributeMap. The
   * returned definition keeps the other options of this definition. It replaces an earlier lock
   * requirement.
   *
   * @example
   * ```ts
   * const cartLock = new Attribute("cart-lock", booleanCodec);
   * const cartItems = new AttributeMap("cart-items", doubleCodec).writesRequireAttributeLock(cartLock);
   *
   * class CartFlow implements Flow<void> {
   *   public getFlowType(): string {
   *     return "CartFlow";
   *   }
   *
   *   public getSteps() {
   *     return StepList.empty();
   *   }
   *
   *   public getPersistenceSchema(): PersistenceSchema {
   *     return { attributes: [cartLock, cartItems] };
   *   }
   *
   *   @rpc({ inputCodec: stringCodec, lockAttributes: [cartLock.lock()] })
   *   public addItem(context: Context, sku: string): void {
   *     cartItems.set(context, sku, 1);
   *   }
   * }
   * ```
   * @param lockAttribute - Singleton Attribute whose lock every instance write requires.
   * @returns A new definition that requires `lockAttribute`'s lock for every instance write; this
   *   definition remains unchanged.
   * @throws {TypeError} When `lockAttribute` is not a singleton {@link Attribute}.
   * @since 1.45.17
   */
  public writesRequireAttributeLock(lockAttribute: Attribute<unknown>): AttributeMap<T> {
    const requiredLockAttribute = requireSingletonLockAttribute(lockAttribute);
    return markLockRequirement(copyAttributeDefinitionOptions(
      this,
      new AttributeMap(this.name, this.codec, this.index),
    ), { kind: "attribute", lockAttribute: requiredLockAttribute });
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

/**
 * Selects one AttributeMap instance for {@link Client.getAttributes}.
 *
 * Create a selection with {@link AttributeMap.instance}. The selection keeps the AttributeMap's
 * value type, so `getAttributes` returns `T | undefined` for it. A selection is immutable and can
 * be reused across calls.
 *
 * @example
 * ```ts
 * const notes = new AttributeMap("notes", jsonCodec<Note>());
 * const firstNote: AttributeMapInstance<Note> = notes.instance("n1");
 * const [note] = await client.getAttributes("order-42", firstNote);
 * ```
 * @typeParam T - Value type of the selected AttributeMap.
 * @since 1.29.0
 */
export class AttributeMapInstance<T> {
  /** Makes the type nominal, so a plain AttributeMapLoad is not accepted as a read. */
  readonly #isAttributeMapInstance = true;

  /**
   * Creates a selection of one AttributeMap instance.
   *
   * Prefer {@link AttributeMap.instance}, which calls this constructor.
   * @param attributeMap - AttributeMap definition. {@link Client.getAttributes} requires the exact
   * definition registered by a Flow in the Client's Registry.
   * @param instance - Non-empty logical map key. Slash is prohibited because it is a reserved
   * character.
   * @throws {TypeError} When `instance` is empty, contains only whitespace, or contains `/`.
   */
  public constructor(
    public readonly attributeMap: AttributeMap<T>,
    public readonly instance: string,
  ) {
    requireMapInstance(instance);
  }
}

/**
 * Selects one value for {@link Client.getAttributes}.
 *
 * Pass a singleton {@link Attribute} directly. Select an AttributeMap instance with
 * {@link AttributeMap.instance}.
 *
 * @example
 * ```ts
 * const reads: readonly AttributeRead<unknown>[] = [orderStatus, notes.instance("n1")];
 * ```
 * @typeParam T - Value type of the selected Attribute or AttributeMap.
 * @since 1.29.0
 */
export type AttributeRead<T> = Attribute<T> | AttributeMapInstance<T>;

/**
 * Maps a tuple of {@link AttributeRead} values to the result tuple of {@link Client.getAttributes}.
 *
 * Each element has the selected value type or `undefined`. `undefined` means the Attribute or
 * AttributeMap instance is not set. The elements keep the order of the reads.
 *
 * @example
 * ```ts
 * type Values = AttributeReadValues<[Attribute<string>, AttributeMapInstance<Note>]>;
 * // Values is [string | undefined, Note | undefined].
 * ```
 * @typeParam Reads - Tuple of reads passed to `getAttributes`.
 * @since 1.29.0
 */
export type AttributeReadValues<Reads extends readonly AttributeRead<unknown>[]> = {
  -readonly [Index in keyof Reads]: Reads[Index] extends infer Read
    ? Read extends Attribute<infer T>
      ? T | undefined
      : Read extends AttributeMapInstance<infer T>
        ? T | undefined
        : never
    : never;
};

/** Declares the Attributes, Channels, and Streams owned by a Flow type. */
export interface PersistenceSchema {
  /** Singleton and map Attribute definitions with unique names. */
  readonly attributes?: readonly (Attribute<unknown> | AttributeMap<unknown>)[];
  /**
   * Singleton Channel, ChannelMap, and Join definitions with unique names.
   *
   * A {@link Join} registers as the ChannelMap that carries its branch results.
   * Changed in 1.44.0: also accepts Join definitions.
   */
  readonly channels?: readonly (Channel<unknown> | ChannelMap<unknown> | Join<unknown>)[];
  /** Best-effort Stream definitions with unique names. */
  readonly streams?: readonly Stream<unknown>[];
}

/** Copies every definition option, so chained option methods keep each other's declarations. */
function copyAttributeDefinitionOptions<Definition extends object>(source: object, copy: Definition): Definition {
  if (isAttributeStoreSynced(source)) {
    markAttributeStoreSynced(copy);
  }
  if (isHardDeleteOnNullDefinition(source)) {
    markHardDeleteOnNull(copy);
  }
  return carryLockRequirement(source, carryPlaintextAttribute(source, copy));
}

function requireSingletonLockAttribute(lockAttribute: unknown): Attribute<unknown> {
  if (!(lockAttribute instanceof Attribute)) {
    throw new TypeError("writesRequireAttributeLock requires a singleton Attribute");
  }
  return lockAttribute as Attribute<unknown>;
}
