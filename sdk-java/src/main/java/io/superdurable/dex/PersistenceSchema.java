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

/**
 * Declares the Attributes, Channels, and Streams that belong to a Flow.
 *
 * <p>Return one schema from {@link Flow#getPersistenceSchema}. Definitions are immutable after the
 * schema is created. The varargs and single-list factories classify definitions automatically;
 * the two-list factory additionally validates that its first list contains only Attributes and its
 * second list contains only Channels.
 *
 * <pre>{@code
 * public PersistenceSchema getPersistenceSchema() {
 *     return PersistenceSchema.of(counter, orderStatus, commands);
 * }
 * }</pre>
 */
public final class PersistenceSchema {
    private final List<PersistenceDefinition> attributes;
    private final List<PersistenceDefinition> channels;
    private final List<PersistenceDefinition> streams;

    private PersistenceSchema(
            final List<? extends PersistenceDefinition> attributes,
            final List<? extends PersistenceDefinition> channels,
            final List<? extends PersistenceDefinition> streams) {
        this.attributes = immutableAttributes(attributes);
        this.channels = immutableChannels(channels);
        this.streams = immutableStreams(streams);
    }

    /**
     * Creates a schema from separate Attribute and Channel lists.
     *
     * @param attributes Attribute and Attribute-map definitions
     * @param channels Channel and Channel-map definitions
     * @return an immutable persistence schema
     * @throws NullPointerException if either list or any definition is {@code null}
     * @throws IllegalArgumentException if a definition appears in the wrong list
     */
    public static PersistenceSchema of(
            final List<? extends PersistenceDefinition> attributes,
            final List<? extends PersistenceDefinition> channels) {
        return new PersistenceSchema(
                attributes,
                channels,
                Collections.<PersistenceDefinition>emptyList());
    }

    /**
     * Creates a schema from separate Attribute, Channel, and Stream lists.
     *
     * @param attributes Attribute and Attribute-map definitions
     * @param channels Channel and Channel-map definitions
     * @param streams Stream definitions
     * @return an immutable persistence schema
     * @throws NullPointerException if a list or definition is {@code null}
     * @throws IllegalArgumentException if a definition appears in the wrong list
     */
    public static PersistenceSchema of(
            final List<? extends PersistenceDefinition> attributes,
            final List<? extends PersistenceDefinition> channels,
            final List<? extends PersistenceDefinition> streams) {
        return new PersistenceSchema(attributes, channels, streams);
    }

    /**
     * Creates a schema by classifying a mixed list of definitions.
     *
     * @param definitions Attribute and Channel definitions in any order
     * @return an immutable persistence schema
     * @throws NullPointerException if the list or any definition is {@code null}
     * @throws IllegalArgumentException if a definition is unsupported
     */
    public static PersistenceSchema of(
            final List<? extends PersistenceDefinition> definitions) {
        Objects.requireNonNull(definitions, "definitions");
        final List<PersistenceDefinition> attributes =
                new ArrayList<PersistenceDefinition>();
        final List<PersistenceDefinition> channels =
                new ArrayList<PersistenceDefinition>();
        final List<PersistenceDefinition> streams =
                new ArrayList<PersistenceDefinition>();
        for (PersistenceDefinition definition : definitions) {
            if (isAttribute(definition)) {
                attributes.add(definition);
            } else if (isChannel(definition)) {
                channels.add(definition);
            } else if (definition instanceof Stream) {
                streams.add(definition);
            } else {
                throw new IllegalArgumentException("unsupported persistence definition");
            }
        }
        return new PersistenceSchema(attributes, channels, streams);
    }

    /**
     * Creates a schema by classifying mixed definitions.
     *
     * @param definitions Attribute and Channel definitions in any order
     * @return an immutable persistence schema
     * @throws NullPointerException if the array or any definition is {@code null}
     * @throws IllegalArgumentException if a definition is unsupported
     */
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

    List<PersistenceDefinition> getStreams() {
        return streams;
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

    private static List<PersistenceDefinition> immutableStreams(
            final List<? extends PersistenceDefinition> definitions) {
        Objects.requireNonNull(definitions, "streams");
        final List<PersistenceDefinition> copy = new ArrayList<PersistenceDefinition>();
        for (PersistenceDefinition definition : definitions) {
            if (!(Objects.requireNonNull(definition, "stream") instanceof Stream)) {
                throw new IllegalArgumentException("streams must contain only Stream definitions");
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
