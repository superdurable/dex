/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;
import java.util.Objects;

public final class PersistenceSchema {
    private final List<PersistenceDefinition> attributes;
    private final List<PersistenceDefinition> channels;

    private PersistenceSchema(
            final List<? extends PersistenceDefinition> attributes,
            final List<? extends PersistenceDefinition> channels) {
        this.attributes = immutableAttributes(attributes);
        this.channels = immutableChannels(channels);
    }

    public static PersistenceSchema of(
            final List<? extends PersistenceDefinition> attributes,
            final List<? extends PersistenceDefinition> channels) {
        return new PersistenceSchema(attributes, channels);
    }

    public static PersistenceSchema of(
            final List<? extends PersistenceDefinition> definitions) {
        Objects.requireNonNull(definitions, "definitions");
        final List<PersistenceDefinition> attributes =
                new ArrayList<PersistenceDefinition>();
        final List<PersistenceDefinition> channels =
                new ArrayList<PersistenceDefinition>();
        for (PersistenceDefinition definition : definitions) {
            if (isAttribute(definition)) {
                attributes.add(definition);
            } else if (isChannel(definition)) {
                channels.add(definition);
            } else {
                throw new IllegalArgumentException("unsupported persistence definition");
            }
        }
        return new PersistenceSchema(attributes, channels);
    }

    public static PersistenceSchema of(final PersistenceDefinition... definitions) {
        Objects.requireNonNull(definitions, "definitions");
        return of(Arrays.asList(definitions));
    }

    List<PersistenceDefinition> getAttributes() {
        return attributes;
    }

    List<PersistenceDefinition> getChannels() {
        return channels;
    }

    private static List<PersistenceDefinition> immutableAttributes(
            final List<? extends PersistenceDefinition> definitions) {
        Objects.requireNonNull(definitions, "attributes");
        final List<PersistenceDefinition> copy = new ArrayList<PersistenceDefinition>();
        for (PersistenceDefinition definition : definitions) {
            if (!isAttribute(Objects.requireNonNull(definition, "attribute"))) {
                throw new IllegalArgumentException(
                        "attributes must contain only Attribute or AttributeMap");
            }
            copy.add(definition);
        }
        return Collections.unmodifiableList(copy);
    }

    private static List<PersistenceDefinition> immutableChannels(
            final List<? extends PersistenceDefinition> definitions) {
        Objects.requireNonNull(definitions, "channels");
        final List<PersistenceDefinition> copy = new ArrayList<PersistenceDefinition>();
        for (PersistenceDefinition definition : definitions) {
            if (!isChannel(Objects.requireNonNull(definition, "channel"))) {
                throw new IllegalArgumentException(
                        "channels must contain only Channel or ChannelMap");
            }
            copy.add(definition);
        }
        return Collections.unmodifiableList(copy);
    }

    private static boolean isAttribute(final PersistenceDefinition definition) {
        return definition instanceof Attribute || definition instanceof AttributeMap;
    }

    private static boolean isChannel(final PersistenceDefinition definition) {
        return definition instanceof Channel || definition instanceof ChannelMap;
    }
}
