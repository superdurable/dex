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