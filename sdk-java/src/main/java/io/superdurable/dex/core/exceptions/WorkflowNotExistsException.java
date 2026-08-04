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

package io.superdurable.dex.core.exceptions;

import io.superdurable.dex.core.ClientSideException;

/**
 * A friendly named exception to indicate that the workflow does not exist
 * It's subclass of {@link ClientSideException} with ErrorSubStatus.WORKFLOW_NOT_EXISTS_SUB_STATUS
 */
public class WorkflowNotExistsException extends ClientSideException {
    public WorkflowNotExistsException(
            final ClientSideException exception) {
        super(exception);
    }
}
