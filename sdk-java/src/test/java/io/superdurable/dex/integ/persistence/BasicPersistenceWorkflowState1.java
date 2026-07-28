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

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.ImmutableStateDecision;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.StateMovement;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.persistence.Persistence;
import io.superdurable.dex.integ.basic.FakContextImpl;

import javax.validation.constraints.AssertTrue;
import java.util.Arrays;

import static io.superdurable.dex.integ.persistence.BasicPersistenceWorkflow.TEST_DATA_OBJECT_KEY;
import static io.superdurable.dex.integ.persistence.BasicPersistenceWorkflow.TEST_DATA_OBJECT_MODEL_1;
import static io.superdurable.dex.integ.persistence.BasicPersistenceWorkflow.TEST_DATA_OBJECT_MODEL_2;
import static io.superdurable.dex.integ.persistence.BasicPersistenceWorkflow.TEST_DATA_OBJECT_PREFIX;
import static io.superdurable.dex.integ.persistence.BasicPersistenceWorkflow.TEST_SEARCH_ATTRIBUTE_DATE_TIME;
import static io.superdurable.dex.integ.persistence.BasicPersistenceWorkflow.TEST_SEARCH_ATTRIBUTE_INT;
import static io.superdurable.dex.integ.persistence.BasicPersistenceWorkflow.TEST_SEARCH_ATTRIBUTE_KEYWORD;

public class BasicPersistenceWorkflowState1 implements WorkflowState<String> {
    public static final String STATE_ID = "query-s1";

    @Override
    public String getStateId() {
        return STATE_ID;
    }

    @Override
    public Class<String> getInputType() {
        return String.class;
    }

    @Override
    public CommandRequest waitUntil(Context context, String input, Persistence persistence, final Communication communication) {
        persistence.setDataAttribute(TEST_DATA_OBJECT_KEY, "query-start");

        // it's allowed to set a child class to a parent
        persistence.setDataAttribute(TEST_DATA_OBJECT_MODEL_1, new FakContextImpl());
        persistence.setDataAttribute(TEST_DATA_OBJECT_MODEL_1, new io.superdurable.dex.gen.models.Context());

        // but it's not allowed to set a parent to child
        //persistence.setDataObject(TEST_DATA_OBJECT_MODEL_2, new io.superdurable.dex.gen.models.Context());
        persistence.setDataAttribute(TEST_DATA_OBJECT_MODEL_2, new FakContextImpl());

        // set dynamic data attribute with the registered prefix
        persistence.setDataAttribute(TEST_DATA_OBJECT_PREFIX + "1", 11L);

        persistence.setStateExecutionLocal("test-key", "test-value-1");
        persistence.recordEvent("event-1", "event-1");
        persistence.recordEvent("event-2", "event-1", 2, "event-3");
        persistence.setSearchAttributeInt64(TEST_SEARCH_ATTRIBUTE_INT, 1L);
        persistence.setSearchAttributeKeyword(TEST_SEARCH_ATTRIBUTE_KEYWORD, "keyword-1");
        // test setting by timestamp
        // 1681766269 is "2023-04-17T14:17:49-07:00" or "2023-04-17T21:17:49-07:00"
        persistence.setSearchAttributeDatetime(TEST_SEARCH_ATTRIBUTE_DATE_TIME, "1681766269");

        return CommandRequest.empty;
    }

    public static final String testDateTimeValue = "2023-04-17T21:17:49-00:00";

    @Override
    public StateDecision execute(Context context, String input, CommandResults commandResults, Persistence persistence, final Communication communication) {
        final String dt = persistence.getSearchAttributeDatetime(TEST_SEARCH_ATTRIBUTE_DATE_TIME);
        if (!dt.equals("1681766269")){
            throw new RuntimeException("datatime is in correct");
        }
        persistence.setSearchAttributeDatetime(TEST_SEARCH_ATTRIBUTE_DATE_TIME, testDateTimeValue);

        String str = persistence.getDataAttribute(TEST_DATA_OBJECT_KEY, String.class);
        persistence.setDataAttribute(TEST_DATA_OBJECT_KEY, str + "-query-decide");

        // it's not allowed to assign a parent to child
        persistence.getDataAttribute(TEST_DATA_OBJECT_MODEL_1, io.superdurable.dex.gen.models.Context.class);
        // but it's allowed to assign child to parent
        persistence.getDataAttribute(TEST_DATA_OBJECT_MODEL_2, io.superdurable.dex.gen.models.Context.class);
        persistence.getDataAttribute(TEST_DATA_OBJECT_MODEL_2, FakContextImpl.class);

        // dynamically created data attribute with the registered prefix
        final Long validDataAttributeWithPrefix = persistence.getDataAttribute(TEST_DATA_OBJECT_PREFIX + "1", Long.class);
        assert validDataAttributeWithPrefix == 11L;

        // return null for non-dynamically created data attribute with the registered prefix
        final Long nonValidDataAttributeWithPrefix = persistence.getDataAttribute(TEST_DATA_OBJECT_PREFIX + "2", Long.class);
        assert nonValidDataAttributeWithPrefix == null;

        String testVal2 = persistence.getStateExecutionLocal("test-key", String.class);
        if (testVal2.equals("test-value-1")) {
            persistence.setStateExecutionLocal("test-key", "test-value-2");
        }
        persistence.recordEvent("event-1", "event-1");
        persistence.recordEvent("event-2", "event-2");

        if (persistence.getSearchAttributeInt64(TEST_SEARCH_ATTRIBUTE_INT) == 1L
                && persistence.getSearchAttributeKeyword(TEST_SEARCH_ATTRIBUTE_KEYWORD).equals("keyword-1")) {
            persistence.setSearchAttributeInt64(TEST_SEARCH_ATTRIBUTE_INT, 2L);
            persistence.setSearchAttributeKeyword(TEST_SEARCH_ATTRIBUTE_KEYWORD, "keyword-2");
        }
        return ImmutableStateDecision.builder()
                .nextStates(Arrays.asList(StateMovement.gracefulCompleteWorkflow("test-value-2")))
                .build();
    }
}
