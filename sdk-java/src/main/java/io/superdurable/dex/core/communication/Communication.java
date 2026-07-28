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

package io.superdurable.dex.core.communication;

import io.superdurable.dex.core.StateMovement;

public interface Communication {

    /**
     * Get the size of the internal channel(including the one being sent in the buffer)
     * NOTE: currently only supported in RPC
     * @param channelName the channel name to get size
     * @return the size of the internal channel
     */
    int getInternalChannelSize(final String channelName);

    /**
     * Get the size of the signal channel(including the one being sent in the buffer)
     * NOTE: currently only supported in RPC
     * @param channelName the channel name to get size
     * @return the size of the signal channel
     */
    int getSignalChannelSize(final String channelName);

    /**
     * Publish a value to an internal Channel
     *
     * @param channelName the channel name to send value
     * @param value       the value to be sent
     */
    void publishInternalChannel(String channelName, Object value);

    /**
     * trigger new state movements as the RPC results
     * NOTE: closing workflows like completing/failing are not supported
     * NOTE: Only used in RPC -- cannot be used in state APIs
     * @param stateMovements the state movements to trigger
     */
    void triggerStateMovements(final StateMovement... stateMovements);
}





