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

package io.superdurable.dex.core;

/**
 * This indicates something goes wrong in the state decision return.
 */
public class InvalidStateDecisionException extends WorkflowDefinitionException {
    public InvalidStateDecisionException(Throwable cause) {
        super(cause);
    }

    public InvalidStateDecisionException(String message) {
        super(message);
    }
}
