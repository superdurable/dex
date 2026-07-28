/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
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
