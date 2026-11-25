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

/** A declared write or publish lock of an Attribute, AttributeMap, Channel, or ChannelMap. */
final class LockRequirement {
    enum Mode {
        /** Requires the lock of the written Attribute or AttributeMap instance. */
        OWN_KEY,
        /** Requires the lock of the singleton Attribute {@link #getLockAttributeName()}. */
        ATTRIBUTE
    }

    private static final LockRequirement OWN_KEY = new LockRequirement(Mode.OWN_KEY, null);

    private final Mode mode;
    private final String lockAttributeName;

    private LockRequirement(final Mode mode, final String lockAttributeName) {
        this.mode = mode;
        this.lockAttributeName = lockAttributeName;
    }

    static LockRequirement ownKey() {
        return OWN_KEY;
    }

    static LockRequirement attribute(final Attribute<?> lockAttribute) {
        Objects.requireNonNull(lockAttribute, "lockAttribute");
        return new LockRequirement(Mode.ATTRIBUTE, lockAttribute.getName());
    }

    Mode getMode() {
        return mode;
    }

    /** Returns the lock Attribute name, or {@code null} for {@link Mode#OWN_KEY}. */
    String getLockAttributeName() {
        return lockAttributeName;
    }
}
