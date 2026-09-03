/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex.exceptions;

/**
 * Reports a selective RPC-state read that the RPC annotation did not request.
 *
 * <p>Add the matching AttributeMap, Channel, or ChannelMap load to the RPC annotation and
 * retry. Channel size metadata and ChannelMap keys remain readable without loading message values.
 */
public final class StateNotLoadedException extends IllegalStateException {
    /**
     * Creates a usage error for one unavailable state load.
     *
     * @param message the load required by the attempted read
     */
    public StateNotLoadedException(final String message) {
        super(message);
    }
}
