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

/**
 * Selects one Attribute value for {@link Client#getAttributes(String, AttributeRead...)}.
 *
 * <p>Two types implement this interface. An {@link Attribute} reads a singleton Attribute. An
 * {@link AttributeMapInstance}, created with {@link AttributeMap#instance(String)}, reads one
 * AttributeMap instance. Pass any mix of them to {@code getAttributes}. Then pass the same read,
 * or an equal one, to {@link AttributeValues#get(AttributeRead)} to obtain its typed value.
 *
 * <p>An {@link AttributeMap} itself is not a read, because a read needs one instance key. A
 * {@link ChannelMap} instance cannot be read. Applications must not implement this interface.
 * {@code getAttributes} rejects any other implementation with {@link IllegalArgumentException}.
 *
 * <pre>{@code
 * Attribute<String> orderStatus = orderFlow.orderStatus;
 * AttributeMapInstance<Note> firstNote = orderFlow.notes.instance("n1");
 *
 * AttributeValues values = client.getAttributes("order-42", orderStatus, firstNote);
 * Optional<String> status = values.get(orderStatus);
 * Optional<Note> note = values.get(orderFlow.notes.instance("n1"));
 * }</pre>
 *
 * @param <T> the Java value type of the selected Attribute or AttributeMap instance
 */
public interface AttributeRead<T> {
}
