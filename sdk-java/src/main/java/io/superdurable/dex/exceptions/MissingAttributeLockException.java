/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex.exceptions;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Objects;

/**
 * Reports a handler invocation that wrote a declared Attribute, or published to a declared
 * Channel, without registering the lock that the definition requires.
 *
 * <p>A definition declares its lock requirement with {@code Attribute.writesRequireLock()},
 * {@code Attribute.writesRequireAttributeLock(lockAttribute)}, the same two methods of
 * {@code AttributeMap}, or {@code publishesRequireAttributeLock(lockAttribute)} of
 * {@code Channel} and {@code ChannelMap}. After a {@code waitFor}, {@code execute}, or RPC handler
 * returns, the Worker compares each such write and publish with the locks that the handler
 * registered. When a required lock is missing, the Worker fails the invocation with this
 * exception. Dex then discards the Attribute writes, Channel publications, and other changes
 * buffered for the invocation's result. Stream writes that the Step already sent and external
 * side effects stay.
 *
 * <p>Dex reports it like any other handler exception. It retries a Step method under the Step's
 * retry policy and returns the failure to the RPC caller. A retry runs the same handler with the
 * same registered locks, so it fails again unless the handler takes another path.
 *
 * <p>Fix the handler registration. Add the lock with {@code StepOptions.Builder.addWaitForLock}
 * or {@code StepOptions.Builder.addExecuteLock} in the options that {@code Step.getStepOptions()}
 * returns, or with {@code lockAttributes} or {@code lockAttributeMaps} of {@code @RPC}. Locks
 * added for one call, such as {@code RPCInvokeOptions.Builder.addLockAttributeMapInstance} or the
 * options of a {@code StepMovement}, do not satisfy the requirement.
 *
 * <p>The exception exists only inside the Worker. A Client that invokes the RPC receives a
 * {@link WorkerInvocationException} whose {@link WorkerInvocationException#getWorkerErrorType()}
 * is {@code "io.superdurable.dex.exceptions.MissingAttributeLockException"} and whose
 * {@link WorkerInvocationException#getWorkerErrorDetail()} is this exception's message. The
 * Worker truncates a detail longer than 1024 UTF-8 bytes.
 *
 * <pre>{@code
 * try {
 *     client.invokeRPC(stub::addItem, item);
 * } catch (WorkerInvocationException exception) {
 *     if (MissingAttributeLockException.class.getName()
 *             .equals(exception.getWorkerErrorType())) {
 *         log.error("register the required lock on the RPC: {}",
 *                 exception.getWorkerErrorDetail());
 *     }
 *     throw exception;
 * }
 * }</pre>
 *
 * @since 1.45.4
 */
public final class MissingAttributeLockException extends RuntimeException {
    private static final long serialVersionUID = 1L;

    /**
     * Identifies the handler method that ran.
     *
     * @since 1.45.4
     */
    public enum HandlerMethod {
        /** A Step {@code waitFor} method. */
        WAIT_FOR("waitFor"),
        /** A Step {@code execute} method. */
        EXECUTE("execute"),
        /** An RPC method. */
        RPC("RPC");

        private final String displayName;

        HandlerMethod(final String displayName) {
            this.displayName = displayName;
        }

        String getDisplayName() {
            return displayName;
        }
    }

    private final String flowType;
    private final String stepType;
    private final HandlerMethod method;
    private final String rpcName;
    private final ArrayList<MissingAttributeLockAccess> accesses;

    /**
     * Creates the exception for one handler invocation.
     *
     * <p>The Worker creates this exception. Applications usually observe it through the Worker
     * error type of a {@link WorkerInvocationException} or a Step's recovery error.
     *
     * @param flowType the Flow type whose handler ran
     * @param stepType the Step type, or {@code null} for an RPC
     * @param method the handler method that ran
     * @param rpcName the RPC name, or {@code null} for a Step method
     * @param accesses every write and publish whose required lock was missing, in the order
     *     described by {@link #getAccesses()}
     * @throws NullPointerException if {@code flowType}, {@code method}, {@code accesses}, or an
     *     access is {@code null}
     */
    public MissingAttributeLockException(
            final String flowType,
            final String stepType,
            final HandlerMethod method,
            final String rpcName,
            final List<MissingAttributeLockAccess> accesses) {
        super(message(flowType, stepType, method, rpcName, accesses));
        this.flowType = flowType;
        this.stepType = stepType;
        this.method = method;
        this.rpcName = rpcName;
        this.accesses = new ArrayList<MissingAttributeLockAccess>(accesses);
    }

    /**
     * Returns the Flow type whose handler ran.
     *
     * @return the nonnull Flow type
     */
    public String getFlowType() {
        return flowType;
    }

    /**
     * Returns the Step type whose method ran.
     *
     * @return the Step type, or {@code null} for an RPC
     */
    public String getStepType() {
        return stepType;
    }

    /**
     * Returns the handler method that ran.
     *
     * @return {@link HandlerMethod#WAIT_FOR}, {@link HandlerMethod#EXECUTE}, or
     *     {@link HandlerMethod#RPC}
     */
    public HandlerMethod getMethod() {
        return method;
    }

    /**
     * Returns the RPC name.
     *
     * @return the RPC name for an RPC invocation, or {@code null} for a Step method
     */
    public String getRpcName() {
        return rpcName;
    }

    /**
     * Returns every write and publish whose required lock was missing.
     *
     * <p>Attribute writes come first, in the order the handler first wrote each key. Channel
     * publishes follow, in the order the handler first published to each Channel or ChannelMap
     * instance.
     *
     * @return an unmodifiable, nonempty list of missing-lock accesses
     */
    public List<MissingAttributeLockAccess> getAccesses() {
        return Collections.unmodifiableList(accesses);
    }

    private static String message(
            final String flowType,
            final String stepType,
            final HandlerMethod method,
            final String rpcName,
            final List<MissingAttributeLockAccess> accesses) {
        Objects.requireNonNull(flowType, "flowType");
        Objects.requireNonNull(method, "method");
        Objects.requireNonNull(accesses, "accesses");
        final StringBuilder message = new StringBuilder("Flow \"").append(flowType).append('"');
        if (method == HandlerMethod.RPC) {
            message.append(" RPC \"").append(rpcName).append('"');
        } else {
            message.append(" Step \"")
                    .append(stepType)
                    .append("\" ")
                    .append(method.getDisplayName());
        }
        for (int index = 0; index < accesses.size(); index++) {
            final MissingAttributeLockAccess access =
                    Objects.requireNonNull(accesses.get(index), "access");
            message.append(index == 0 ? " " : ", ").append(access);
        }
        return message.append("; register the lock in the handler's StepOptions or @RPC locks")
                .toString();
    }
}
