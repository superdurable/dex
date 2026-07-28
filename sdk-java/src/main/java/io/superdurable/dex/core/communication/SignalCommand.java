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

import io.superdurable.dex.core.command.BaseCommand;
import org.immutables.value.Value;

@Value.Immutable
public abstract class SignalCommand implements BaseCommand {

    public abstract String getSignalChannelName();

    /**
     * Create one signal command.
     *
     * @param commandId    required.
     * @param signalName   required.
     * @return signal command
     */
    public static SignalCommand create(final String commandId, final String signalName) {
        return ImmutableSignalCommand.builder()
                .commandId(commandId)
                .signalChannelName(signalName)
                .build();
    }

    /**
     * Create one signal command.
     *
     * @param signalName     required.
     * @return signal command
     */
    public static SignalCommand create(final String signalName) {
        return ImmutableSignalCommand.builder()
                .signalChannelName(signalName)
                .build();
    }
}