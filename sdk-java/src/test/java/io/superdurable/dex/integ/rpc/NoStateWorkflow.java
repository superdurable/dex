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

package io.superdurable.dex.integ.rpc;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.RPC;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.persistence.DataAttributeDef;
import io.superdurable.dex.core.persistence.Persistence;
import io.superdurable.dex.core.persistence.PersistenceFieldDef;
import io.superdurable.dex.gen.models.PersistenceLoadingType;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

import static io.superdurable.dex.integ.RpcTest.RPC_OUTPUT;

@Component
public class NoStateWorkflow implements ObjectWorkflow {

    public static final String DA_COUNTER = "counter";
    private RpcWorkflow rpcWorkflow;
    private NoStartStateWorkflow noStartStateWorkflow;

    @Autowired
    public NoStateWorkflow(RpcWorkflow rpcWorkflow, NoStartStateWorkflow noStartStateWorkflow) {
        this.rpcWorkflow = rpcWorkflow;
        this.noStartStateWorkflow = noStartStateWorkflow;
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(Integer.class, DA_COUNTER)
        );
    }

    @RPC(
            dataAttributesLoadingType = PersistenceLoadingType.PARTIAL_WITH_EXCLUSIVE_LOCK,
            dataAttributesPartialLoadingKeys = {DA_COUNTER},
            dataAttributesLockingKeys = {DA_COUNTER}
    )
    public String increaseCounter(Context context, Persistence persistence, Communication communication) {
        Integer current = persistence.getDataAttribute(DA_COUNTER, Integer.class);
        if (current == null) {
            current = 0;
        }
        current++;
        persistence.setDataAttribute(DA_COUNTER, current);
        return "done";
    }

    @RPC
    public String testWrite(Context context, Persistence persistence, Communication communication) {
        persistence.setDataAttribute(DA_COUNTER, 123);
        return "done";
    }

    @RPC
    public Integer getCounter(Context context, Persistence persistence, Communication communication) {
        return persistence.getDataAttribute(DA_COUNTER, Integer.class);
    }

    @RPC
    public Long testRpcFunc1(Context context, String input, Persistence persistence, Communication communication) {
        if (context.getWorkflowId().isEmpty() || context.getWorkflowRunId().isEmpty()) {
            throw new RuntimeException("invalid context");
        }
        return RPC_OUTPUT;
    }

    @RPC
    public Long testRpcFunc1Error(Context context, String input, Persistence persistence, Communication communication) {
        throw new RuntimeException("this is an error");
    }
}
