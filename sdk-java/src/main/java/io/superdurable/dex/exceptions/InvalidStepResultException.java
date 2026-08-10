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
 * Reports an invalid value returned by user Step code.
 *
 * <p>The worker raises this specialized {@link FlowDefinitionException} for results it cannot send
 * to Dex, including a {@code null} wait or decision, unsupported wait shape, invalid transition
 * target, or malformed conditional completion. Fix the Step implementation before retrying.
 */
public final class InvalidStepResultException extends FlowDefinitionException {
    /**
     * Creates an invalid-result exception with the violated Step contract.
     *
     * @param message the user-actionable result violation
     */
    public InvalidStepResultException(final String message) {
        super(message);
    }
}
