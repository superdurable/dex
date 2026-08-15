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

/**
 * Defines the Steps, persistence, and RPC surface of one Flow type.
 *
 * <p>Applications implement this interface on a concrete class and register an instance with
 * {@link Registry}. The generic parameter is the input accepted by the start Step; the typed
 * {@link StepList#startStep} factory enforces that relationship at compile time. A Flow may omit a
 * start Step and begin only through an RPC. The default Flow type is the concrete class's simple
 * name, so explicit named classes make that type predictable and reviewable.
 *
 * <pre>{@code
 * final class OrderFlow implements Flow<OrderInput> {
 *     private final ValidateOrder start = new ValidateOrder();
 *     private final ChargeOrder charge = new ChargeOrder();
 *
 *     @Override
 *     public StepList<OrderInput> getSteps() {
 *         return StepList.startStep(start).otherSteps(charge);
 *     }
 * }
 * }</pre>
 *
 * @param <StartInput> the Java input type accepted by the Flow's start Step
 */
public interface Flow<StartInput> {
    /**
     * Returns the Flow type used to register and start this Flow.
     *
     * <p>The default is {@code getClass().getSimpleName()}. Override it when refactoring the Java
     * class name must not change the Flow type stored by Dex.
     *
     * @return the nonblank Flow type
     */
    default String getFlowType() {
        return getClass().getSimpleName();
    }

    /**
     * Returns the Flow's start Step and all other registered Steps.
     *
     * <p>The default is an empty list, which is valid for an RPC-only Flow. Every Step targeted by a
     * decision or RPC result must appear in this list.
     *
     * @return the immutable typed Step list; never {@code null}
     */
    default StepList<StartInput> getSteps() {
        return StepList.empty();
    }

    /**
     * Returns the persistent Attributes and Channels used by this Flow.
     *
     * <p>The default schema is empty. Every persistence definition used by Steps or RPC methods must
     * be registered here.
     *
     * @return the Flow's immutable persistence schema; never {@code null}
     */
    default PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
    }

    /**
     * Handles expiration of this Flow's soft timeout.
     *
     * <p>Override this method to make a positive timeout default to
     * {@link FlowTimeoutPolicy#HANDLER}. Dex calls it at most once after the durable timeout timer
     * completes or is skipped. The returned decision may transition to another Step, end without
     * closing, complete, fail, or request graceful completion. The Context belongs to this
     * invocation and must not be retained.
     *
     * @param context the timeout-handler invocation Context
     * @return the non-null decision applied with normal Step Execute semantics
     * @throws UnsupportedOperationException when called without an application override
     */
    default StepDecision handleTimeout(final Context context) {
        throw new UnsupportedOperationException("Flow has no timeout handler");
    }
}
