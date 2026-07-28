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

package io.superdurable.dex.integ.persistence;

import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.StateDef;
import io.superdurable.dex.core.persistence.PersistenceFieldDef;
import io.superdurable.dex.core.persistence.SearchAttributeDef;
import io.superdurable.dex.gen.models.SearchAttributeValueType;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

@Component
public class SetSearchAttributeWorkflow implements ObjectWorkflow {
    public static final String SEARCH_ATTRIBUTE_KEYWORD = "CustomKeywordField";
    public static final String SEARCH_ATTRIBUTE_TEXT = "CustomTextField";
    public static final String SEARCH_ATTRIBUTE_DOUBLE = "CustomDoubleField";
    public static final String SEARCH_ATTRIBUTE_INT = "CustomIntField";
    public static final String SEARCH_ATTRIBUTE_BOOL = "CustomBoolField";
    public static final String SEARCH_ATTRIBUTE_KEYWORD_ARRAY = "CustomKeywordArrayField";
    public static final String SEARCH_ATTRIBUTE_DATE_TIME = "CustomDatetimeField";

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(StateDef.startingState(new SetSearchAttributeWorkflowState1()));
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                SearchAttributeDef.create(SearchAttributeValueType.INT, SEARCH_ATTRIBUTE_INT),
                SearchAttributeDef.create(SearchAttributeValueType.KEYWORD, SEARCH_ATTRIBUTE_KEYWORD),
                SearchAttributeDef.create(SearchAttributeValueType.DATETIME, SEARCH_ATTRIBUTE_DATE_TIME),
                SearchAttributeDef.create(SearchAttributeValueType.TEXT, SEARCH_ATTRIBUTE_TEXT),
                SearchAttributeDef.create(SearchAttributeValueType.DOUBLE, SEARCH_ATTRIBUTE_DOUBLE),
                SearchAttributeDef.create(SearchAttributeValueType.BOOL, SEARCH_ATTRIBUTE_BOOL),
                SearchAttributeDef.create(SearchAttributeValueType.KEYWORD_ARRAY, SEARCH_ATTRIBUTE_KEYWORD_ARRAY)
        );
    }
}
