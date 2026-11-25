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

import io.superdurable.dex.exceptions.MissingAttributeLockAccess;
import io.superdurable.dex.exceptions.MissingAttributeLockException;
import io.superdurable.dex.exceptions.MissingAttributeLockException.HandlerMethod;
import io.superdurable.gen.AttributeWrite;
import io.superdurable.gen.ChannelMessage;

import java.io.UnsupportedEncodingException;
import java.net.URLDecoder;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;

/** Enforces declared write and publish lock requirements against registration-time locks. */
final class RequiredAttributeLockCheck {
    private RequiredAttributeLockCheck() {
    }

    /**
     * Fails a successful handler whose buffered writes or publishes lack a declared lock.
     *
     * <p>Runs after the handler returned and before its result is sent. Per-call locks never reach
     * the Worker, so only {@code heldLockKeys} from registration count.
     */
    static void check(
            final Registry.RegisteredFlow flow,
            final String stepType,
            final HandlerMethod method,
            final String rpcName,
            final Set<String> heldLockKeys,
            final List<AttributeWrite> attributeWrites,
            final List<ChannelMessage> publications) {
        final Map<String, PersistenceDefinition> persistence = flow.getPersistence();
        final List<MissingAttributeLockAccess> missing = new ArrayList<MissingAttributeLockAccess>();
        for (AttributeWrite write : attributeWrites) {
            final String physical = write.getKey();
            final String name = definitionName(physical);
            final PersistenceDefinition definition = persistence.get(name);
            final LockRequirement requirement =
                    definition == null ? null : definition.getLockRequirement();
            if (requirement == null) {
                continue;
            }
            final boolean ownKey = requirement.getMode() == LockRequirement.Mode.OWN_KEY;
            final String lockKey = ownKey ? physical : requirement.getLockAttributeName();
            if (heldLockKeys.contains(lockKey)) {
                continue;
            }
            final String instance = instance(physical);
            missing.add(new MissingAttributeLockAccess(
                    definition instanceof AttributeMap
                            ? MissingAttributeLockAccess.Kind.ATTRIBUTE_MAP
                            : MissingAttributeLockAccess.Kind.ATTRIBUTE,
                    name,
                    instance,
                    ownKey ? name : requirement.getLockAttributeName(),
                    ownKey ? instance : null));
        }
        final Set<String> published = new HashSet<String>();
        for (ChannelMessage publication : publications) {
            final String physical = publication.getChannelName();
            if (!published.add(physical)) {
                continue;
            }
            final String name = definitionName(physical);
            final PersistenceDefinition definition = persistence.get(name);
            final LockRequirement requirement =
                    definition == null ? null : definition.getLockRequirement();
            if (requirement == null || heldLockKeys.contains(requirement.getLockAttributeName())) {
                continue;
            }
            missing.add(new MissingAttributeLockAccess(
                    definition instanceof ChannelMap
                            ? MissingAttributeLockAccess.Kind.CHANNEL_MAP
                            : MissingAttributeLockAccess.Kind.CHANNEL,
                    name,
                    instance(physical),
                    requirement.getLockAttributeName(),
                    null));
        }
        if (!missing.isEmpty()) {
            throw new MissingAttributeLockException(
                    flow.getName(), stepType, method, rpcName, missing);
        }
    }

    private static String definitionName(final String physical) {
        final int separator = physical.indexOf('/');
        return separator < 0 ? physical : physical.substring(0, separator);
    }

    /** Returns the decoded map instance of a stored key, or {@code null} for a singleton. */
    private static String instance(final String physical) {
        final int separator = physical.indexOf('/');
        if (separator < 0) {
            return null;
        }
        final String escaped = physical.substring(separator + 1);
        try {
            return URLDecoder.decode(escaped, "UTF-8");
        } catch (UnsupportedEncodingException | IllegalArgumentException undecodable) {
            return escaped;
        }
    }
}
