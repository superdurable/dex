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

package io.superdurable.dex.core.mapper;

import io.superdurable.dex.gen.models.SignalCommand;

public class SignalCommandMapper {
    public static SignalCommand toGenerated(io.superdurable.dex.core.communication.SignalCommand signalCommand) {
        final SignalCommand command = new SignalCommand()
                .signalChannelName(signalCommand.getSignalChannelName());
        if (signalCommand.getCommandId().isPresent()) {
            command.commandId(signalCommand.getCommandId().get());
        }
        return command;
    }
}
