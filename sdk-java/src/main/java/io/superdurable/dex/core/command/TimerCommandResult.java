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

package io.superdurable.dex.core.command;

import io.superdurable.dex.gen.models.TimerStatus;
import org.immutables.value.Value;

@Value.Immutable
public abstract class TimerCommandResult {

    public abstract TimerStatus getTimerStatus();

    public abstract String getCommandId();
}