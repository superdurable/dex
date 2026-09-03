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

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Marks a public Flow method as an RPC endpoint.
 *
 * <p>An RPC method may accept {@link Context} alone or {@code Context} followed by one concrete
 * input type. It may return {@code void} or a typed {@link RPCResult}. The registry derives the
 * RPC name from the Java method unless {@link #name} is set. Flow classes that expose RPCs
 * must be non-final, RPC methods must be non-final, and Kotlin users must declare both with
 * {@code open}; the client creates a ByteBuddy stub that intercepts direct method references
 * without running the Flow constructor.
 *
 * <pre>{@code
 * @RPC(lockAttributes = {"balance"})
 * public RPCResult<Integer> addCredit(Context context, Integer amount) {
 *     int updated = balance.get(context) + amount;
 *     balance.set(context, updated);
 *     return RPCResult.of(updated);
 * }
 * }</pre>
 */
@Retention(RetentionPolicy.RUNTIME)
@Target(ElementType.METHOD)
public @interface RPC {
    /**
     * Overrides the RPC name.
     *
     * @return the explicit name, or an empty string to use the Java method name
     */
    String name() default "";

    /**
     * Sets the RPC execution timeout in whole seconds.
     *
     * @return the timeout in seconds, or zero to use the server default
     */
    int timeoutSeconds() default 0;

    /**
     * Names Attributes to lock for the RPC invocation.
     *
     * @return registered Attribute names; defaults to an empty array
     */
    String[] lockAttributes() default {};

    /**
     * Selects Attribute-map instances to lock for the RPC invocation.
     *
     * @return map-instance lock annotations; defaults to an empty array
     */
    RPCAttributeMapLock[] lockAttributeMaps() default {};

    /**
     * Requests transactional reads and writes without requiring Attribute locks.
     *
     * <p>Set this for an RPC that deletes a Channel message when a missing ID must prevent every
     * other staged write from committing.
     *
     * @return {@code true} to request transactional execution; defaults to {@code false}
     */
    boolean isTransactional() default false;

    /**
     * Selects AttributeMap entries included in the RPC snapshot.
     *
     * <p>Use {@code MapName/} for every current instance or {@code MapName/instance} for one
     * logical instance. The SDK escapes the instance portion before sending the request.
     *
     * @return registered AttributeMap selectors; defaults to an empty array
     */
    String[] loadAttributeMaps() default {};

    /**
     * Selects Channels whose pending messages are included in the RPC snapshot.
     *
     * @return registered singleton Channel names; defaults to an empty array
     */
    String[] loadChannels() default {};

    /**
     * Selects ChannelMap pending messages included in the RPC snapshot.
     *
     * <p>Use {@code MapName/} for every current instance or {@code MapName/instance} for one
     * logical instance. The SDK escapes the instance portion before sending the request.
     *
     * @return registered ChannelMap selectors; defaults to an empty array
     */
    String[] loadChannelMaps() default {};
}
