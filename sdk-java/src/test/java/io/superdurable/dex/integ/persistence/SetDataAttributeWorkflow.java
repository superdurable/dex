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
import io.superdurable.dex.gen.models.Context;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

@Component
public class SetDataAttributeWorkflow implements ObjectWorkflow {
    public static final String DATA_OBJECT_KEY = "data-obj-key-1";
    public static final String DATA_OBJECT_MODEL_KEY = "data-obj-1";
    public static final String DATA_OBJECT_KEY_PREFIX = "data-obj-key-prefix-";

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(StateDef.startingState(new SetDataAttributeWorkflowState1()));
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(String.class, DATA_OBJECT_KEY),
                DataAttributeDef.create(Context.class, DATA_OBJECT_MODEL_KEY),
                DataAttributeDef.createByPrefix(Long.class, DATA_OBJECT_KEY_PREFIX)
        );
    }
}
