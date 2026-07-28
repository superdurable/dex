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
import io.superdurable.dex.core.persistence.DataAttributeDef;
import io.superdurable.dex.core.persistence.PersistenceFieldDef;
import io.superdurable.dex.core.persistence.SearchAttributeDef;
import io.superdurable.dex.gen.models.Context;
import io.superdurable.dex.gen.models.SearchAttributeValueType;
import io.superdurable.dex.integ.basic.FakContextImpl;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

@Component
public class BasicPersistenceWorkflow implements ObjectWorkflow {
    public static final String TEST_INIT_DATA_OBJECT_KEY = "data-obj-0";
    public static final String TEST_DATA_OBJECT_KEY = "data-obj-1";
    public static final String TEST_DATA_OBJECT_MODEL_1 = "data-obj-2";

    public static final String TEST_DATA_OBJECT_MODEL_2 = "data-obj-3";
    public static final String TEST_DATA_OBJECT_PREFIX = "data-obj-prefix-";

    public static final String TEST_SEARCH_ATTRIBUTE_KEYWORD = "CustomKeywordField";
    public static final String TEST_SEARCH_ATTRIBUTE_INT = "CustomIntField";

    public static final String TEST_SEARCH_ATTRIBUTE_DATE_TIME = "CustomDatetimeField";

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(StateDef.startingState(new BasicPersistenceWorkflowState1()));
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(String.class, TEST_INIT_DATA_OBJECT_KEY),
                DataAttributeDef.create(String.class, TEST_DATA_OBJECT_KEY),
                DataAttributeDef.create(Context.class, TEST_DATA_OBJECT_MODEL_1),
                DataAttributeDef.create(FakContextImpl.class, TEST_DATA_OBJECT_MODEL_2),
                DataAttributeDef.createByPrefix(Long.class, TEST_DATA_OBJECT_PREFIX),
                SearchAttributeDef.create(SearchAttributeValueType.INT, TEST_SEARCH_ATTRIBUTE_INT),
                SearchAttributeDef.create(SearchAttributeValueType.KEYWORD, TEST_SEARCH_ATTRIBUTE_KEYWORD),
                SearchAttributeDef.create(SearchAttributeValueType.DATETIME, TEST_SEARCH_ATTRIBUTE_DATE_TIME)
        );
    }
}
