/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex.exceptions;

import java.io.Serializable;
import java.util.Objects;

/**
 * Names one write or publish whose required lock the handler did not register.
 *
 * <p>{@link MissingAttributeLockException#getAccesses()} returns one value for each such write or
 * publish. Each value names the written Attribute or AttributeMap instance, or the Channel or
 * ChannelMap instance that received a publish, and the lock that its definition requires.
 *
 * <pre>{@code
 * for (MissingAttributeLockAccess access : exception.getAccesses()) {
 *     log.warn("{} {} needs the lock of {}",
 *             access.getKind(), access.getName(), access.getLockAttributeName());
 * }
 * }</pre>
 *
 * @since 1.45.4
 */
public final class MissingAttributeLockAccess implements Serializable {
    private static final long serialVersionUID = 1L;

    /**
     * Identifies the kind of definition that the handler wrote or published to.
     *
     * @since 1.45.4
     */
    public enum Kind {
        /** A singleton Attribute write. */
        ATTRIBUTE("Attribute"),
        /** A write of one AttributeMap instance. */
        ATTRIBUTE_MAP("AttributeMap"),
        /** A publish to a singleton Channel. */
        CHANNEL("Channel"),
        /** A publish to one ChannelMap instance. */
        CHANNEL_MAP("ChannelMap");

        private final String displayName;

        Kind(final String displayName) {
            this.displayName = displayName;
        }

        String getDisplayName() {
            return displayName;
        }

        boolean isPublish() {
            return this == CHANNEL || this == CHANNEL_MAP;
        }
    }

    private final Kind kind;
    private final String name;
    private final String instance;
    private final String lockAttributeName;
    private final String lockInstance;

    /**
     * Creates one missing-lock access.
     *
     * <p>The Worker creates these values. Applications read them from
     * {@link MissingAttributeLockException#getAccesses()}.
     *
     * @param kind the kind of definition written or published to
     * @param name the Attribute, AttributeMap, Channel, or ChannelMap name
     * @param instance the AttributeMap or ChannelMap instance, or {@code null} for a singleton
     *     definition
     * @param lockAttributeName the name of the Attribute or AttributeMap whose lock is required
     * @param lockInstance the AttributeMap instance whose lock is required, or {@code null} when
     *     the required lock is a singleton Attribute's lock
     * @throws NullPointerException if {@code kind}, {@code name}, or {@code lockAttributeName} is
     *     {@code null}
     */
    public MissingAttributeLockAccess(
            final Kind kind,
            final String name,
            final String instance,
            final String lockAttributeName,
            final String lockInstance) {
        this.kind = Objects.requireNonNull(kind, "kind");
        this.name = Objects.requireNonNull(name, "name");
        this.instance = instance;
        this.lockAttributeName = Objects.requireNonNull(lockAttributeName, "lockAttributeName");
        this.lockInstance = lockInstance;
    }

    /**
     * Returns the kind of definition written or published to.
     *
     * @return the nonnull definition kind
     */
    public Kind getKind() {
        return kind;
    }

    /**
     * Returns the name of the definition written or published to.
     *
     * @return the Attribute, AttributeMap, Channel, or ChannelMap name
     */
    public String getName() {
        return name;
    }

    /**
     * Returns the map instance written or published to.
     *
     * @return the decoded AttributeMap or ChannelMap instance key, or {@code null} for a singleton
     *     Attribute or Channel
     */
    public String getInstance() {
        return instance;
    }

    /**
     * Returns the name of the definition whose lock is required.
     *
     * @return the lock Attribute name for a coordination requirement, or the written Attribute or
     *     AttributeMap name for an own-key requirement
     */
    public String getLockAttributeName() {
        return lockAttributeName;
    }

    /**
     * Returns the AttributeMap instance whose lock is required.
     *
     * @return the decoded AttributeMap instance key when the written instance's own lock is
     *     required, or {@code null} when the required lock is a singleton Attribute's lock
     */
    public String getLockInstance() {
        return lockInstance;
    }

    /**
     * Describes this access the way the exception message does.
     *
     * @return a sentence fragment such as
     *     {@code wrote Attribute "total" without the required lock of Attribute "total"}
     */
    @Override
    public String toString() {
        final StringBuilder description = new StringBuilder()
                .append(kind.isPublish() ? "published to " : "wrote ")
                .append(kind.getDisplayName())
                .append(" \"")
                .append(name)
                .append('"');
        if (instance != null) {
            description.append(" instance \"").append(instance).append('"');
        }
        description.append(" without the required lock of ");
        if (lockInstance == null) {
            description.append("Attribute \"").append(lockAttributeName).append('"');
        } else {
            description.append("AttributeMap \"")
                    .append(lockAttributeName)
                    .append("\" instance \"")
                    .append(lockInstance)
                    .append('"');
        }
        return description.toString();
    }

    /**
     * Compares all fields of two accesses.
     *
     * @param other the object to compare
     * @return {@code true} when {@code other} is an access with equal fields
     */
    @Override
    public boolean equals(final Object other) {
        if (this == other) {
            return true;
        }
        if (!(other instanceof MissingAttributeLockAccess)) {
            return false;
        }
        final MissingAttributeLockAccess access = (MissingAttributeLockAccess) other;
        return kind == access.kind
                && name.equals(access.name)
                && Objects.equals(instance, access.instance)
                && lockAttributeName.equals(access.lockAttributeName)
                && Objects.equals(lockInstance, access.lockInstance);
    }

    /**
     * Returns a hash code consistent with {@link #equals(Object)}.
     *
     * @return the hash of all fields
     */
    @Override
    public int hashCode() {
        return Objects.hash(kind, name, instance, lockAttributeName, lockInstance);
    }
}
