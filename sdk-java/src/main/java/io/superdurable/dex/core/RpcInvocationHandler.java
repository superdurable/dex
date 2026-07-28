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

import io.superdurable.dex.core.persistence.PersistenceOptions;
import io.superdurable.dex.gen.models.PersistenceLoadingPolicy;
import io.superdurable.dex.gen.models.PersistenceLoadingType;
import io.superdurable.dex.gen.models.SearchAttributeKeyAndType;
import net.bytebuddy.implementation.bind.annotation.AllArguments;
import net.bytebuddy.implementation.bind.annotation.Origin;
import net.bytebuddy.implementation.bind.annotation.RuntimeType;

import java.lang.reflect.Method;
import java.util.Arrays;
import java.util.List;

import static io.superdurable.dex.core.RpcDefinitions.*;

public class RpcInvocationHandler {

    private final String workflowId;
    private final String workflowRunId;

    final UnregisteredClient unregisteredClient;

    final PersistenceOptions schemaOptions;

    final List<SearchAttributeKeyAndType> searchAttributeKeyAndTypes;

    public RpcInvocationHandler(final UnregisteredClient unregisteredClient, final String workflowId, final String workflowRunId, final PersistenceOptions schemaOptions, final List<SearchAttributeKeyAndType> searchAttributeKeyAndTypes) {
        this.unregisteredClient = unregisteredClient;
        this.workflowId = workflowId;
        this.workflowRunId = workflowRunId;
        this.schemaOptions = schemaOptions;
        this.searchAttributeKeyAndTypes = searchAttributeKeyAndTypes;
    }

    @RuntimeType
    public Object intercept(@AllArguments Object[] allArguments,
                            @Origin Method method) {
        final RPC rpcAnno = method.getAnnotation(RPC.class);
        if (rpcAnno == null) {
            throw new WorkflowDefinitionException("An RPC method must be annotated by RPC annotation");
        }

        RpcMethodMetadata metadata = RpcMethodMatcher.match(method);
        if (metadata == null) {
            throw new WorkflowDefinitionException("An RPC method must be annotated by RPC annotation");
        }
        Object input = metadata.hasInput() ? allArguments[metadata.getInputIndex()] : null;

        final Class<?> outputType = method.getReturnType();

        boolean useMemo = schemaOptions.getEnableCaching();
        if (rpcAnno.bypassCachingForStrongConsistency()) {
            useMemo = false;
        }

        if (metadata.usesPersistence()) {
            return unregisteredClient.invokeRpc(
                    outputType,
                    input,
                    workflowId,
                    workflowRunId,
                    method.getName(),
                    rpcAnno.timeoutSeconds(),
                    new PersistenceLoadingPolicy()
                            .persistenceLoadingType(rpcAnno.dataAttributesLoadingType())
                            .partialLoadingKeys(Arrays.asList(rpcAnno.dataAttributesPartialLoadingKeys()))
                            .lockingKeys(Arrays.asList(rpcAnno.dataAttributesLockingKeys())),
                    new PersistenceLoadingPolicy()
                            .persistenceLoadingType(rpcAnno.searchAttributesLoadingType())
                            .lockingKeys(Arrays.asList(rpcAnno.searchAttributesLockingKeys()))
                            .partialLoadingKeys(Arrays.asList(rpcAnno.searchAttributesPartialLoadingKeys())),
                    useMemo,
                    searchAttributeKeyAndTypes
            );
        } else {
            return unregisteredClient.invokeRpc(
                    outputType,
                    input,
                    workflowId,
                    workflowRunId,
                    method.getName(),
                    rpcAnno.timeoutSeconds(),
                    new PersistenceLoadingPolicy()
                            .persistenceLoadingType(PersistenceLoadingType.NONE),
                    new PersistenceLoadingPolicy()
                            .persistenceLoadingType(PersistenceLoadingType.NONE),
                    useMemo,
                    null);
        }

    }
}