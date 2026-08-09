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

import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Selects one Attribute-map instance to lock during an RPC invocation.
 *
 * <p>Use this nested annotation from {@link RPC#lockAttributeMaps}. Both values are durable names,
 * not Java field names.
 *
 * <pre>{@code
 * @RPC(lockAttributeMaps = {
 *     @RPCAttributeMapLock(attribute = "order-status", instance = "order-123")
 * })
 * public RPCResult<String> readStatus(Context context) { ... }
 * }</pre>
 */
@Retention(RetentionPolicy.RUNTIME)
@Target({})
public @interface RPCAttributeMapLock {
    /**
     * Names the registered Attribute map.
     *
     * @return the durable Attribute-map name
     */
    String attribute();

    /**
     * Names the map instance to lock.
     *
     * @return the durable Attribute-map instance
     */
    String instance();
}
