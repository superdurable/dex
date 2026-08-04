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
import io.superdurable.dex.gen.models.PersistenceLoadingType;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * This is for annotating an RPC method for an implementation of {@link ObjectWorkflow}
 * The method must be in the form of one of {@link RpcDefinitions}
 * An RPC implementation can call any APIs to update external systems directly.
 * However, it can also trigger some state execution (using {@link io.superdurable.dex.core.communication.Communication} API)
 * to update in the background to ensure the consistency across systems.
 */
@Retention(RetentionPolicy.RUNTIME)
@Target(ElementType.METHOD)
public @interface RPC {
    int timeoutSeconds() default 0;

    PersistenceLoadingType dataAttributesLoadingType() default PersistenceLoadingType.ALL_WITHOUT_LOCKING;

    // used when dataAttributesLoadingType is PARTIAL_WITHOUT_LOCKING
    String[] dataAttributesPartialLoadingKeys() default {};

    String[] dataAttributesLockingKeys() default {};

    PersistenceLoadingType searchAttributesLoadingType() default PersistenceLoadingType.ALL_WITHOUT_LOCKING;

    // used when searchAttributesPartialLoadingKeys is PARTIAL_WITHOUT_LOCKING
    String[] searchAttributesPartialLoadingKeys() default {};

    String[] searchAttributesLockingKeys() default {};

    /**
     * Only used when workflow has enabled {@link PersistenceOptions} CachingPersistenceByMemo
     * By default, it's false for high throughput support
     * flip to true to bypass the caching for strong consistent reads
     * @return true if bypass caching for strong consistency
     */
    boolean bypassCachingForStrongConsistency() default false;
}