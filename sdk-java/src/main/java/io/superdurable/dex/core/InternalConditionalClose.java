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

import io.superdurable.dex.gen.models.WorkflowConditionalCloseType;
import org.immutables.value.Value;

import java.util.Optional;

@Value.Immutable
public abstract class InternalConditionalClose {
    public abstract WorkflowConditionalCloseType getWorkflowConditionalCloseType();

    public abstract String getChannelName();

    public abstract Optional<Object> getCloseInput();
}
