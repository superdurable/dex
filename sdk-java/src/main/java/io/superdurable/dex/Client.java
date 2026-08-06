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

package io.superdurable.dex;

import java.time.Duration;
import java.util.List;

public final class Client implements AutoCloseable {
    private final Registry registry;
    private final BlobCache blobCache;
    private final ClientOptions options;

    public Client(final Registry registry, final BlobCache blobCache) {
        this(registry, blobCache, new ClientOptions());
    }

    public Client(
            final Registry registry,
            final BlobCache blobCache,
            final ClientOptions options) {
        if (registry == null || blobCache == null || options == null) {
            throw new IllegalArgumentException("registry, blobCache, and options are required");
        }
        this.registry = registry;
        this.blobCache = blobCache;
        this.options = options;
    }

    Registry getRegistry() {
        return registry;
    }

    BlobCache getBlobCache() {
        return blobCache;
    }

    ClientOptions getOptions() {
        return options;
    }

    public <I> String startFlow(
            final Flow<I> flow,
            final String flowId,
            final I input) {
        return startFlow(flow, flowId, input, new StartFlowOptions());
    }

    public <I> String startFlow(
            final Flow<I> flow,
            final String flowId,
            final I input,
            final StartFlowOptions startOptions) {
        throw laterPhase("Client transport");
    }

    public <T> T newRpcStub(
            final Class<T> rpcClass,
            final String flowId) {
        return newRpcStub(rpcClass, flowId, "");
    }

    public <T> T newRpcStub(
            final Class<T> rpcClass,
            final String flowId,
            final String runId) {
        throw laterPhase("RPCStub proxy");
    }

    public <I, O> O invokeRPC(
            final RpcDefinitions.RpcFunc1<I, O> rpcStubMethod,
            final I input) {
        throw laterPhase("Client transport");
    }

    public <O> O invokeRPC(final RpcDefinitions.RpcFunc0<O> rpcStubMethod) {
        throw laterPhase("Client transport");
    }

    public <I> void invokeRPC(
            final RpcDefinitions.RpcProc1<I> rpcStubMethod,
            final I input) {
        throw laterPhase("Client transport");
    }

    public void invokeRPC(final RpcDefinitions.RpcProc0 rpcStubMethod) {
        throw laterPhase("Client transport");
    }

    public <T> T getAttribute(
            final String flowId,
            final String runId,
            final Attribute<T> attribute) {
        throw laterPhase("Client transport");
    }

    public <T> T getAttribute(
            final String flowId,
            final Attribute<T> attribute) {
        throw laterPhase("Client transport");
    }

    public <T> T getAttribute(
            final String flowId,
            final AttributeMap<T> attribute,
            final String instance) {
        throw laterPhase("Client transport");
    }

    public <T> void setAttribute(
            final String flowId,
            final String runId,
            final Attribute<T> attribute,
            final T value) {
        throw laterPhase("Client transport");
    }

    public <T> void setAttribute(
            final String flowId,
            final Attribute<T> attribute,
            final T value) {
        throw laterPhase("Client transport");
    }

    public <T> void setAttribute(
            final String flowId,
            final AttributeMap<T> attribute,
            final String instance,
            final T value) {
        throw laterPhase("Client transport");
    }

    public <T> void publish(
            final String flowId,
            final String runId,
            final Channel<T> channel,
            final T value) {
        throw laterPhase("Client transport");
    }

    @SafeVarargs
    public final <T> void publish(
            final String flowId,
            final Channel<T> channel,
            final T... values) {
        throw laterPhase("Client transport");
    }

    @SafeVarargs
    public final <T> void publish(
            final String flowId,
            final ChannelMap<T> channel,
            final String instance,
            final T... values) {
        throw laterPhase("Client transport");
    }

    public <T> void publish(
            final String flowId,
            final Channel<T> channel,
            final List<T> values) {
        throw laterPhase("Client transport");
    }

    public void stopFlow(final String flowId) {
        stopFlow(flowId, new StopFlowOptions());
    }

    public void stopFlow(final String flowId, final StopFlowOptions stopOptions) {
        throw laterPhase("Client transport");
    }

    public void waitForFlow(final String flowId) {
        throw laterPhase("Client transport");
    }

    public <O> O waitForFlow(final String flowId, final Class<O> outputType) {
        throw laterPhase("Client transport");
    }

    public <O> O waitForFlow(
            final String flowId,
            final Class<O> outputType,
            final Duration timeout) {
        throw laterPhase("Client transport");
    }

    public FlowInfo describeFlow(final String flowId) {
        throw laterPhase("Client transport");
    }

    public String resetFlow(final String flowId, final ResetFlowOptions resetOptions) {
        throw laterPhase("Client transport");
    }

    public void skipTimer(
            final String flowId,
            final StepExecutionId stepExecutionId,
            final TimerId timerId) {
        throw laterPhase("Client transport");
    }

    public void waitForStepCompletion(
            final String flowId,
            final StepExecutionId stepExecutionId,
            final Duration timeout) {
        throw laterPhase("Client transport");
    }

    public void updateFlowConfig(final String flowId, final FlowConfig config) {
        throw laterPhase("Client transport");
    }

    public void triggerContinueAsNew(final String flowId) {
        throw laterPhase("Client transport");
    }

    @Override
    public void close() {
        throw laterPhase("Client transport");
    }

    private static PhaseNotImplementedException laterPhase(final String component) {
        return new PhaseNotImplementedException(component + " belongs to a later phase");
    }
}
