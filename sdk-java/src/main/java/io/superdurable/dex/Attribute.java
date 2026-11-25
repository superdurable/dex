/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

import java.util.Objects;

/**
 * Defines one durable, typed value stored with a Flow execution.
 *
 * <p>Declare Attributes once on the Flow, include them in its {@link PersistenceSchema}, and use
 * the same object from Step and RPC code. Values are serialized using the {@link Class} supplied at
 * definition time. A parameterized value type such as {@code List<String>} cannot be supplied
 * directly because {@code List.class} does not retain its element type. Wrap the value in a
 * concrete class whose field is a {@code List<String>}, or use {@code String[]} when an array fits
 * the data model.
 *
 * <p>Writes through a handler Context are staged until its successful result is committed;
 * reads in that invocation also see its staged writes. A failed invocation does not commit them.
 * External readers use {@link Client#getAttributes(String, AttributeRead...)} for committed state.
 * Concurrent writers that need serialization must participate in {@link AttributeLock} locking.
 * Declare {@link #writesRequireLock()} or {@link #writesRequireAttributeLock(Attribute)} to make
 * the Worker fail a writer that does not register the lock.
 *
 * <pre>{@code
 * private final Attribute<Integer> counter =
 *         Attribute.define("counter", Integer.class);
 * private final Attribute<Tags> tags =
 *         Attribute.define("tags", Tags.class);
 * private final Attribute<String[]> tagArray =
 *         Attribute.define("tag-array", String[].class);
 *
 * static final class Tags {
 *     public List<String> values;
 * }
 *
 * public StepDecision execute(Context context, Command input) {
 *     counter.set(context, counter.get(context) + 1);
 *     return StepDecision.gracefulComplete();
 * }
 * }</pre>
 *
 * @param <T> the Java value type stored in the Attribute
 */
public final class Attribute<T> extends PersistenceDefinition implements AttributeRead<T> {
    private final String name;
    private final Class<T> valueType;
    private final AttributeIndex index;
    private final boolean syncToAttributeStore;
    private final boolean hardDeleteOnNull;
    private final LockRequirement writeLock;

    private Attribute(
            final String name,
            final Class<T> valueType,
            final AttributeIndex index,
            final boolean syncToAttributeStore,
            final boolean hardDeleteOnNull,
            final LockRequirement writeLock) {
        this.name = requirePersistenceDefinitionName(name);
        this.valueType = Objects.requireNonNull(valueType, "valueType");
        this.index = index;
        this.syncToAttributeStore = syncToAttributeStore;
        this.hardDeleteOnNull = hardDeleteOnNull;
        this.writeLock = writeLock;
    }

    /**
     * Defines an Attribute without a search index.
     *
     * @param name the stable Attribute name; must not be blank or contain {@code /}
     * @param valueType the concrete Java class used to serialize and deserialize values
     * @param <T> the Java value type
     * @return the typed Attribute definition
     * @throws IllegalArgumentException if {@code name} is {@code null}, blank, or contains {@code /}
     * @throws NullPointerException if {@code valueType} is {@code null}
     */
    public static <T> Attribute<T> define(final String name, final Class<T> valueType) {
        return new Attribute<T>(name, valueType, null, false, false, null);
    }

    /**
     * Defines an Attribute with a search index.
     *
     * @param name the stable Attribute name; must not be blank or contain {@code /}
     * @param valueType the concrete Java class used to serialize and deserialize values
     * @param index the search-index definition, or {@code null} for no index
     * @param <T> the Java value type
     * @return the typed Attribute definition
     * @throws IllegalArgumentException if {@code name} is {@code null}, blank, or contains {@code /}
     * @throws NullPointerException if {@code valueType} is {@code null}
     */
    public static <T> Attribute<T> define(
            final String name,
            final Class<T> valueType,
            final AttributeIndex index) {
        return new Attribute<T>(name, valueType, index, false, false, null);
    }

    /**
     * Returns a definition whose writes are projected through the Flow's Attribute Store.
     *
     * <p>The projection is asynchronous and latest-state only. A projection failure does not roll
     * back the Flow Attribute. Deleting this Attribute projects SQL {@code NULL} when the target
     * column permits it.
     *
     * @return a new immutable Attribute definition with Attribute Store synchronization enabled
     * @throws IllegalStateException if this definition uses {@link #hardDeleteOnNull()}
     */
    public Attribute<T> syncToAttributeStore() {
        if (hardDeleteOnNull) {
            throw new IllegalStateException(
                    "hard-delete Attribute " + name + " cannot use syncToAttributeStore");
        }
        return new Attribute<T>(name, valueType, index, true, false, writeLock);
    }

    /**
     * Returns a definition whose value is stored only in the Dex blob store and is permanently
     * deleted when the Attribute is deleted or set to {@code null}.
     *
     * <p>Use this option for short-lived secrets, such as an API key or an access token, that a Flow
     * needs for a limited time. A regular Attribute value is recorded in Flow history. A hard-delete
     * Attribute value is never recorded in Flow history. Dex stores it only in the blob store and
     * records only a reference in Flow history. Calling {@link #delete(Context)} or
     * {@link #set(Context, Object)} with {@code null} permanently deletes the stored value. After
     * the deletion, the Attribute reads as absent.
     *
     * <p>Only string and object values are supported. Object values include JSON-mapped classes and
     * {@code byte[]}. Numeric and Boolean value types, such as {@link Integer}, {@link Long},
     * {@link Double}, and {@link Boolean}, are rejected when this option is applied. When the
     * declared value type is broader, such as {@link Object} or {@link Number}, a write whose
     * value maps to a number or a Boolean is rejected when it is written. Writing {@code null} is
     * always allowed.
     *
     * <p>This option cannot be combined with an {@link AttributeIndex} or with
     * {@link #syncToAttributeStore()}. {@link Client#waitForAttributeMatch} rejects a hard-delete
     * Attribute before it contacts the Server. Use an RPC that reads the Attribute when a caller
     * needs the value. {@link Client#getAttributes(String, AttributeRead...)} also reads a
     * hard-delete Attribute's committed value.
     *
     * <p>The Dex Server must have an enabled blob store and must support Server protocol version 3
     * or later. A Step, RPC, StartFlow, or SubFlow start that writes this Attribute fails with
     * {@link IllegalStateException} when the negotiated Server protocol is older. The Server rejects
     * the write when its blob store is disabled. Steps and RPC handlers receive the value inline.
     * The SDK never stores a hard-delete value in the local {@link BlobCache}.
     *
     * <p>Know these limitations before you store a secret:
     *
     * <ul>
     *   <li>Each Flow keeps one stored copy per Attribute key. Every write overwrites that copy in
     *       place.</li>
     *   <li>A Step or RPC attempt writes or deletes the stored value before Dex commits the attempt's
     *       result. A retried attempt, other Steps, and other readers can therefore observe a write
     *       or a deletion from an attempt that later failed.</li>
     *   <li>When concurrent Steps or RPCs write the same key, the final stored value may not be the
     *       value from the last committed write. When the order matters, use a single writer, or
     *       make every Step and RPC that writes the key hold an {@link AttributeLock} on it. A
     *       failed locked attempt can still overwrite the value.</li>
     *   <li>A {@link Client#startFlow} call or {@link SubFlow#run} start that fails because the
     *       Flow ID is already running still
     *       overwrites the running Flow's value. With a {@code null} initial value, it deletes the
     *       running Flow's value.</li>
     *   <li>Executions that reuse a Flow ID share the stored value.</li>
     *   <li>{@link Client#timeTravel} does not restore a deleted value. A deleted value reads as absent.</li>
     *   <li>A restarted SubFlow starts from the original initial value. If an
     *       earlier run deleted that value, the new run reads it as absent.</li>
     *   <li>Removing {@code hardDeleteOnNull()} from a definition leaves the stored value in place
     *       until Dex removes the Flow. Delete the value with the hard-delete definition first.</li>
     *   <li>A value remains in the blob store until it is deleted, or until Flow retention removes
     *       the Flow and blob cleanup runs.</li>
     *   <li>Blob store backups and bucket versioning can retain copies after deletion. Configure
     *       them separately when deleted secrets must not survive.</li>
     * </ul>
     *
     * <pre>{@code
     * final class ProviderSyncFlow implements Flow<String> {
     *     final Attribute<String> apiKey =
     *             Attribute.define("api-key", String.class).hardDeleteOnNull();
     *     private final SyncWithProvider sync = new SyncWithProvider(apiKey);
     *
     *     @Override
     *     public StepList<String> getSteps() {
     *         return StepList.startStep(sync);
     *     }
     *
     *     @Override
     *     public PersistenceSchema getPersistenceSchema() {
     *         return PersistenceSchema.of(apiKey);
     *     }
     * }
     *
     * final class SyncWithProvider implements Step<String> {
     *     private final Attribute<String> apiKey;
     *
     *     SyncWithProvider(final Attribute<String> apiKey) {
     *         this.apiKey = apiKey;
     *     }
     *
     *     @Override
     *     public StepDecision execute(final Context context, final String accountId) {
     *         final String key = apiKey.get(context);
     *         ProviderApi.sync(accountId, key);
     *         // Permanently delete the secret once it is no longer needed.
     *         apiKey.delete(context);
     *         return StepDecision.gracefulComplete();
     *     }
     * }
     *
     * // Start the Flow with the secret as an initial Attribute value.
     * client.startFlow(
     *         flow,
     *         "provider-sync-42",
     *         "account-42",
     *         StartFlowOptions.newBuilder()
     *                 .addAttribute(flow.apiKey, System.getenv("PROVIDER_API_KEY"))
     *                 .build());
     * }</pre>
     *
     * @return a new immutable Attribute definition with hard deletion on {@code null} enabled
     * @throws IllegalStateException if this definition has an index, uses
     *     {@link #syncToAttributeStore()}, or declares a numeric or Boolean value type
     */
    public Attribute<T> hardDeleteOnNull() {
        HardDeleteAttributes.requireDefinitionSupport(
                name, valueType, index, syncToAttributeStore);
        return new Attribute<T>(name, valueType, index, false, true, writeLock);
    }

    /**
     * Returns a definition whose every write requires holding this Attribute's own lock.
     *
     * <p>Two Steps or RPCs that read a value and then write it can overwrite each other's update
     * unless both hold the same lock. Declare the requirement on the definition, and the Worker
     * enforces it. A {@code waitFor}, {@code execute}, or RPC invocation that writes this Attribute
     * with {@link #set(Context, Object)} or {@link #delete(Context)} without holding
     * {@link AttributeLock#of(Attribute)} for it fails with
     * {@link io.superdurable.dex.exceptions.MissingAttributeLockException}. Dex then discards the
     * changes buffered for the invocation's result. The check is not a warning; calling this method
     * opts in to it. Reads are not checked.
     *
     * <p>Only the locks a handler registers count: {@link StepOptions.Builder#addWaitForLock} or
     * {@link StepOptions.Builder#addExecuteLock} in the options that {@link Step#getStepOptions()}
     * returns, and {@link RPC#lockAttributes} for an RPC. The Worker reads a Step's options once,
     * when the {@link Registry} is built. Locks added for one call do not count:
     * {@link RPCInvokeOptions.Builder#addLockAttributeMapInstance} and the options passed to
     * {@link StepMovement#of(Class, Object, StepOptions)} cannot satisfy the requirement. Register
     * the lock instead. A Flow timeout handler is not checked, because its locks come from
     * {@link StartFlowOptions}. Attribute values set when a Flow starts are not checked either.
     *
     * <p>The check compares writes with the registered locks, not with the locks Dex acquires for
     * the call. Options passed with a {@link StepMovement} replace the target Step's whole options,
     * and Dex takes an RPC's locks from the Client's definition. Keep the required lock in such
     * options, and build every Client and Worker from the same definitions. A Worker built without
     * this declaration does not check it.
     *
     * <p>Use {@link #writesRequireAttributeLock(Attribute)} instead to require the lock of a shared
     * coordination Attribute. When both methods are chained, the last call wins. The declaration
     * is kept by {@link #syncToAttributeStore()} and {@link #hardDeleteOnNull()}. Register the
     * returned definition in the Flow's {@link PersistenceSchema}, not the original one.
     *
     * <pre>{@code
     * public final class OrderFlow implements Flow<Integer> {
     *     final Attribute<Integer> orderTotal =
     *             Attribute.define("order-total", Integer.class).writesRequireLock();
     *     private final AddItem addItem = new AddItem(orderTotal);
     *
     *     @Override
     *     public StepList<Integer> getSteps() {
     *         return StepList.startStep(addItem);
     *     }
     *
     *     @Override
     *     public PersistenceSchema getPersistenceSchema() {
     *         return PersistenceSchema.of(orderTotal);
     *     }
     * }
     *
     * final class AddItem implements Step<Integer> {
     *     private final Attribute<Integer> orderTotal;
     *
     *     AddItem(final Attribute<Integer> orderTotal) {
     *         this.orderTotal = orderTotal;
     *     }
     *
     *     @Override
     *     public StepOptions getStepOptions() {
     *         return StepOptions.newBuilder()
     *                 .addExecuteLock(AttributeLock.of(orderTotal))
     *                 .build();
     *     }
     *
     *     @Override
     *     public StepDecision execute(final Context context, final Integer price) {
     *         orderTotal.set(context, orderTotal.get(context) + price);
     *         return StepDecision.gracefulComplete();
     *     }
     * }
     * }</pre>
     *
     * @return a new immutable Attribute definition whose writes require this Attribute's own lock
     * @since 1.45.4
     */
    public Attribute<T> writesRequireLock() {
        return new Attribute<T>(
                name, valueType, index, syncToAttributeStore, hardDeleteOnNull,
                LockRequirement.ownKey());
    }

    /**
     * Returns a definition whose every write requires holding the lock of the singleton Attribute
     * {@code lockAttribute}.
     *
     * <p>Use it when several values share one invariant and one coordination Attribute protects
     * them all. The rules of {@link #writesRequireLock()} apply: only locks that the handler
     * registers count, and a write without the lock fails the invocation with
     * {@link io.superdurable.dex.exceptions.MissingAttributeLockException}. Each writer must
     * register {@code AttributeLock.of(lockAttribute)}. The coordination Attribute needs no stored
     * value.
     *
     * <p>{@code lockAttribute} must be registered in the same Flow's {@link PersistenceSchema}.
     * {@link Registry} construction fails with
     * {@link io.superdurable.dex.exceptions.FlowDefinitionException} when no Attribute with that
     * name is registered there, or when the name belongs to an AttributeMap. When both methods are
     * chained, the last call wins.
     *
     * <pre>{@code
     * final Attribute<Boolean> balanceLock = Attribute.define("balance-lock", Boolean.class);
     * final Attribute<Integer> balance =
     *         Attribute.define("balance", Integer.class).writesRequireAttributeLock(balanceLock);
     *
     * // In every Step that writes balance:
     * return StepOptions.newBuilder()
     *         .addExecuteLock(AttributeLock.of(balanceLock))
     *         .build();
     *
     * // In every RPC that writes balance:
     * @RPC(lockAttributes = {"balance-lock"})
     * public void addCredit(final Context context, final Integer amount) {
     *     balance.set(context, balance.get(context) + amount);
     * }
     * }</pre>
     *
     * @param lockAttribute the singleton coordination Attribute whose lock every write requires
     * @return a new immutable Attribute definition whose writes require the lock of
     *     {@code lockAttribute}
     * @throws NullPointerException if {@code lockAttribute} is {@code null}
     * @since 1.45.4
     */
    public Attribute<T> writesRequireAttributeLock(final Attribute<?> lockAttribute) {
        return new Attribute<T>(
                name, valueType, index, syncToAttributeStore, hardDeleteOnNull,
                LockRequirement.attribute(lockAttribute));
    }

    @Override
    LockRequirement getLockRequirement() {
        return writeLock;
    }

    String getName() {
        return name;
    }

    Class<T> getValueType() {
        return valueType;
    }

    AttributeIndex getIndex() {
        return index;
    }

    @Override
    boolean isSyncToAttributeStore() {
        return syncToAttributeStore;
    }

    @Override
    boolean isHardDeleteOnNull() {
        return hardDeleteOnNull;
    }

    /**
     * Reads this Attribute from the current invocation.
     *
     * <p>The read includes writes staged earlier in the same invocation. Reading an Attribute that
     * has no value does not throw. It returns {@code false} when the value type is {@code
     * boolean} or {@code Boolean}. It returns zero when the value type is {@code byte}, {@code
     * short}, {@code int}, {@code long}, {@code float}, or {@code double}, or the matching wrapper
     * class {@code Byte}, {@code Short}, {@code Integer}, {@code Long}, {@code Float}, or {@code
     * Double}. It returns {@code null} for every other value type, including {@code BigDecimal},
     * {@code BigInteger}, and {@code String}. A value that was deleted, or set to {@code null},
     * reads the same way.
     *
     * <p>A read of one of those fourteen types therefore cannot tell an unset value from a stored
     * zero or {@code false}. When that difference matters, use a {@code String} or object value
     * type, or keep a separate Attribute that records whether the value was set.
     *
     * <pre>{@code
     * public final class OrderFlow implements Flow<Void> {
     *     public final Attribute<String> reviewStatus =
     *             Attribute.define("review-status", String.class);
     *
     *     @Override
     *     public StepList<Void> getSteps() {
     *         return StepList.empty();
     *     }
     *
     *     @Override
     *     public PersistenceSchema getPersistenceSchema() {
     *         return PersistenceSchema.of(reviewStatus);
     *     }
     *
     *     @RPC
     *     public RPCResult<String> getReviewStatus(final Context context) {
     *         final String status = reviewStatus.get(context);
     *         return RPCResult.of(status == null ? "pending" : status);
     *     }
     * }
     * }</pre>
     *
     * @param context the Step or RPC invocation context
     * @return the current typed value; when no value is stored, {@code false} or zero for the
     *     fourteen primitive and wrapper types listed above, and {@code null} for any other value
     *     type
     * @throws IllegalArgumentException if this definition is not registered in the current Flow's
     *     {@link PersistenceSchema}
     * @throws io.superdurable.dex.exceptions.ValueMappingException if the stored value cannot be
     *     decoded as the value type
     */
    public T get(final Context context) {
        return context.getAttribute(this);
    }

    /**
     * Writes this Attribute in the current invocation.
     *
     * @param context the Step or RPC invocation context
     * @param value the typed value to persist; {@code null} deletes the value
     * @throws IllegalArgumentException if this is a {@link #hardDeleteOnNull()} Attribute and the
     *     value maps to a number or a Boolean
     */
    public void set(final Context context, final T value) {
        context.setAttribute(this, value);
    }

    /**
     * Deletes this Attribute in the current invocation.
     *
     * @param context the Step or RPC invocation context
     */
    public void delete(final Context context) {
        context.deleteAttribute(this);
    }

    static String requireName(final String name) {
        if (name == null || name.trim().isEmpty()) {
            throw new IllegalArgumentException("name is required");
        }
        return name;
    }

    static String requirePersistenceDefinitionName(final String name) {
        final String value = requireName(name);
        if (value.contains("/")) {
            throw new IllegalArgumentException("persistence definition names must not contain '/'");
        }
        return value;
    }

    static String requireMapInstance(final String instance) {
        final String value = requireName(instance);
        if (value.contains("/")) {
            throw new IllegalArgumentException("map instances must not contain '/'");
        }
        return value;
    }
}
