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

package io.superdurable.dex.core.persistence;

import io.superdurable.dex.gen.models.SearchAttributeValueType;
import org.immutables.value.Value;

@Value.Immutable
public abstract class SearchAttributeDef implements PersistenceFieldDef {

    public abstract SearchAttributeValueType getSearchAttributeType();

    /**
     * The search attribute types are all from Cadence/Temporal
     * See doc https://cadenceworkflow.io/docs/concepts/search-workflows/ and https://docs.temporal.io/concepts/what-is-a-search-attribute/
     * to understand how to register new search attributes and run query
     * NOTE that KEYWORD_ARRAY should be registered as KEYWORD in Cadence/Temporal. Cadence/Temporal use it interchangably. But in DEX, we like things to be explicit.
     *
     * @param attributeType the type
     * @param key           the key
     * @return the definition
     */
    public static SearchAttributeDef create(SearchAttributeValueType attributeType, String key) {
        return ImmutableSearchAttributeDef.builder()
                .key(key)
                .searchAttributeType(attributeType)
                .build();
    }
}
