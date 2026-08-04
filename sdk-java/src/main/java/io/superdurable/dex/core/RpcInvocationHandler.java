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