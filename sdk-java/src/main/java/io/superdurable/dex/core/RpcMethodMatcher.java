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

package io.superdurable.dex.core;

import com.google.common.collect.ImmutableMap;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.persistence.Persistence;

import java.lang.reflect.Method;
import java.util.Map;

class RpcMethodMatcher {
    public static final Map<Integer, Class<?>> RPC_WITH_INPUT_PERSISTENCE_PARAM_TYPES =
            new ImmutableMap.Builder<Integer, Class<?>>()
                    .put(0, Context.class)
                    .put(2, Persistence.class)
                    .put(3, Communication.class)
                    .build();
    public static final RpcMethodMetadata METADATA_RPC_WITH_INPUT_PERSISTENCE =
            ImmutableRpcMethodMetadata.builder()
                    .hasInput(true)
                    .inputIndex(1)
                    .usesPersistence(true)
                    .build();

    public static final Map<Integer, Class<?>> RPC_WITH_INPUT_PARAM_TYPES =
            new ImmutableMap.Builder<Integer, Class<?>>()
                    .put(0, Context.class)
                    .put(2, Communication.class)
                    .build();
    public static final RpcMethodMetadata METADATA_RPC_WITH_INPUT =
            ImmutableRpcMethodMetadata.builder()
                    .hasInput(true)
                    .inputIndex(1)
                    .usesPersistence(false)
                    .build();

    public static final Map<Integer, Class<?>> RPC_WITH_PERSISTENCE_PARAM_TYPES =
            new ImmutableMap.Builder<Integer, Class<?>>()
                    .put(0, Context.class)
                    .put(1, Persistence.class)
                    .put(2, Communication.class)
                    .build();
    public static final RpcMethodMetadata METADATA_RPC_WITH_PERSISTENCE =
            ImmutableRpcMethodMetadata.builder()
                    .hasInput(false)
                    .inputIndex(-1)
                    .usesPersistence(true)
                    .build();

    public static final Map<Integer, Class<?>> RPC_WITHOUT_INPUT_PERSISTENCE_PARAM_TYPES =
            new ImmutableMap.Builder<Integer, Class<?>>()
                    .put(0, Context.class)
                    .put(1, Communication.class)
                    .build();
    public static final RpcMethodMetadata METADATA_RPC_WITHOUT_INPUT_PERSISTENCE =
            ImmutableRpcMethodMetadata.builder()
                    .hasInput(false)
                    .inputIndex(-1)
                    .usesPersistence(false)
                    .build();

    private static final int RPC_PARAM_COUNT_MAX = 4;
    private static final int RPC_PARAM_COUNT_MIN = 2;

    public static RpcMethodMetadata match(Method method) {
        final Class<?>[] paramTypes = method.getParameterTypes();
        if (paramTypes.length < RPC_PARAM_COUNT_MIN || paramTypes.length > RPC_PARAM_COUNT_MAX) {
            return null;
        }

        switch (paramTypes.length) {
            case 2:
                if (validateInputParameters(paramTypes, RPC_WITHOUT_INPUT_PERSISTENCE_PARAM_TYPES)) {
                    return METADATA_RPC_WITHOUT_INPUT_PERSISTENCE;
                } else {
                    return null;
                }
            case 3:
                if (validateInputParameters(paramTypes, RPC_WITH_PERSISTENCE_PARAM_TYPES)) {
                    return METADATA_RPC_WITH_PERSISTENCE;
                } else if (validateInputParameters(paramTypes, RPC_WITH_INPUT_PARAM_TYPES)) {
                    return METADATA_RPC_WITH_INPUT;
                } else {
                    return null;
                }
            case 4:
                if (validateInputParameters(paramTypes, RPC_WITH_INPUT_PERSISTENCE_PARAM_TYPES)) {
                    return METADATA_RPC_WITH_INPUT_PERSISTENCE;
                } else {
                    return null;
                }
        }

        return null;
    }

    private static boolean validateInputParameters(Class<?>[] paramTypes, Map<Integer, Class<?>> expectedInputParamTypes) {
        for (Map.Entry<Integer, Class<?>> entry: expectedInputParamTypes.entrySet()) {
            if (entry.getKey() >= paramTypes.length) {
                return false;
            }

            if (!paramTypes[entry.getKey()].equals(entry.getValue())) {
                return false;
            }
        }

        return true;
    }
}
