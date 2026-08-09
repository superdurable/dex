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
 * Reports an invalid local Flow, Step, persistence, or RPC definition.
 *
 * <p>This exception is raised before or while mapping a request when the Java contract cannot be
 * represented safely, such as a duplicate durable name, an unsupported RPC signature, a final RPC
 * class, or a value whose runtime type does not match its registered definition. Fix application
 * definitions rather than retrying the operation unchanged.
 */
public class FlowDefinitionException extends RuntimeException {
    /**
     * Creates a definition failure without an underlying cause.
     *
     * @param message the user-actionable contract violation
     */
    public FlowDefinitionException(final String message) {
        super(message);
    }

    /**
     * Creates a definition failure caused by reflection or mapping work.
     *
     * @param message the user-actionable contract violation
     * @param cause the underlying local failure
     */
    public FlowDefinitionException(final String message, final Throwable cause) {
        super(message, cause);
    }
}
