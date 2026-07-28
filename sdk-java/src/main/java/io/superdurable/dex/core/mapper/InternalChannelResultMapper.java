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

import io.superdurable.dex.core.ObjectEncoder;
import io.superdurable.dex.core.communication.ImmutableInternalChannelCommandResult;
import io.superdurable.dex.core.communication.InternalChannelCommandResult;
import io.superdurable.dex.gen.models.InterStateChannelResult;

import java.util.Optional;

public class InternalChannelResultMapper {
    public static InternalChannelCommandResult fromGenerated(
            InterStateChannelResult result,
            Class<?> type,
            ObjectEncoder objectEncoder) {
        return ImmutableInternalChannelCommandResult.builder()
                .commandId(result.getCommandId())
                .requestStatusEnum(result.getRequestStatus())
                .channelName(result.getChannelName())
                .value(Optional.ofNullable(objectEncoder.decode(result.getValue(), type)))
                .build();
    }
}
