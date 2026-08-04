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

package io.superdurable.dex.core.persistence;

public interface StateExecutionLocals {
    /**
     * set a local attribute. The scope of the attribute is only within the execution of this state.
     * Usually it's for passing from State Start API to State Decide API
     * User code must make sure using the same type for both get and set
     *
     * @param key   the key of the stateExecutionLocal(scope of the state execution)
     * @param value the value
     */
    void setStateExecutionLocal(String key, Object value);

    /**
     * Retrieve a local state attribute
     * User code must make sure using the same type for both get and set
     *
     * @param key  the key of the stateExecutionLocal(scope of the state execution)
     * @param type the value type
     * @param <T>  the value type
     * @return the value
     */
    <T> T getStateExecutionLocal(String key, Class<T> type);

    /**
     * Record an arbitrary event in State Start/Decide API for debugging/tracking purpose
     *
     * @param key       the key of the event. Within a Start/Decide API, the same key cannot be used for more than once.
     * @param eventData the data of the event.
     */
    void recordEvent(String key, Object... eventData);
}
