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
