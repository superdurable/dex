/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

final class HandlerStateLoads {
    private final List<AttributeMap<?>> attributeMaps;
    private final List<MapInstance> attributeMapInstances;
    private final List<Channel<?>> channels;
    private final List<ChannelMap<?>> channelMaps;
    private final List<MapInstance> channelMapInstances;

    private HandlerStateLoads(final Builder builder) {
        attributeMaps = immutable(builder.attributeMaps);
        attributeMapInstances = immutable(builder.attributeMapInstances);
        channels = immutable(builder.channels);
        channelMaps = immutable(builder.channelMaps);
        channelMapInstances = immutable(builder.channelMapInstances);
    }

    List<AttributeMap<?>> getAttributeMaps() {
        return attributeMaps;
    }

    List<MapInstance> getAttributeMapInstances() {
        return attributeMapInstances;
    }

    List<Channel<?>> getChannels() {
        return channels;
    }

    List<ChannelMap<?>> getChannelMaps() {
        return channelMaps;
    }

    List<MapInstance> getChannelMapInstances() {
        return channelMapInstances;
    }

    private static <T> List<T> immutable(final List<T> values) {
        return Collections.unmodifiableList(new ArrayList<T>(values));
    }

    static final class Builder {
        private final List<AttributeMap<?>> attributeMaps = new ArrayList<AttributeMap<?>>();
        private final List<MapInstance> attributeMapInstances = new ArrayList<MapInstance>();
        private final List<Channel<?>> channels = new ArrayList<Channel<?>>();
        private final List<ChannelMap<?>> channelMaps = new ArrayList<ChannelMap<?>>();
        private final List<MapInstance> channelMapInstances = new ArrayList<MapInstance>();

        void addAttributeMap(final AttributeMap<?> definition) {
            attributeMaps.add(definition);
        }

        void addAttributeMapInstance(final AttributeMap<?> definition, final String instance) {
            attributeMapInstances.add(new MapInstance(definition, instance));
        }

        void addChannel(final Channel<?> definition) {
            channels.add(definition);
        }

        void addChannelMap(final ChannelMap<?> definition) {
            channelMaps.add(definition);
        }

        void addChannelMapInstance(final ChannelMap<?> definition, final String instance) {
            channelMapInstances.add(new MapInstance(definition, instance));
        }

        HandlerStateLoads build() {
            return new HandlerStateLoads(this);
        }
    }

    static final class MapInstance {
        private final PersistenceDefinition definition;
        private final String instance;

        private MapInstance(
                final PersistenceDefinition definition,
                final String instance) {
            this.definition = definition;
            this.instance = instance;
        }

        PersistenceDefinition getDefinition() {
            return definition;
        }

        String getInstance() {
            return instance;
        }
    }
}
