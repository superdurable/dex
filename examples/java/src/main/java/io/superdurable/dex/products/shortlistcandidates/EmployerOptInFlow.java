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

package io.superdurable.dex.products.shortlistcandidates;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeIndex;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import org.springframework.stereotype.Component;

@Component
public class EmployerOptInFlow implements Flow<EmployerOptInInput> {
    public final Attribute<String> employerId = Attribute.define(
            "EMPLOYER_OPT_IN_EmployerId",
            String.class,
            new AttributeIndex(AttributeIndex.Type.KEYWORD, "CustomKeywordField"));
    public final Attribute<Boolean> optedIn = Attribute.define(
            "EMPLOYER_OPT_IN_Status",
            Boolean.class);

    private final OptIn optIn = new OptIn();
    private final OptOut optOut = new OptOut();

    @Override
    public StepList<EmployerOptInInput> getSteps() {
        return StepList.startStep(optIn).otherSteps(optOut);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(employerId, optedIn);
    }

    @RPC
    public RPCResult<Boolean> isOptedIn(final Context context) {
        return RPCResult.of(Boolean.TRUE.equals(optedIn.get(context)));
    }

    @RPC
    public RPCResult<Void> optOut(final Context context) {
        return RPCResult.of(null, StepMovement.of(optOut, null));
    }

    final class OptIn implements Step<EmployerOptInInput> {
        @Override
        public Class<EmployerOptInInput> getInputType() {
            return EmployerOptInInput.class;
        }

        @Override
        public StepDecision execute(final Context context, final EmployerOptInInput input) {
            employerId.set(context, input.employerId);
            optedIn.set(context, true);
            return StepDecision.deadEnd();
        }
    }

    final class OptOut implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            optedIn.set(context, false);
            return StepDecision.forceComplete();
        }
    }
}
