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

package io.superdurable.dex.products.dealdsl;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.ChannelMap;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

@Component
public class DealDSLFlow implements Flow<DealStart> {
    public final Attribute<DealDefinition> definition =
            Attribute.define("DealDefinition", DealDefinition.class);
    public final Attribute<Map> stateData = Attribute.define("DealStateData", Map.class);
    public final Attribute<String> processId = Attribute.define("DealProcessID", String.class);
    public final Attribute<String> itemId = Attribute.define("DealItemID", String.class);
    public final Attribute<String> buyerId = Attribute.define("DealBuyerID", String.class);
    public final Attribute<String> currentState =
            Attribute.define("DealCurrentState", String.class);
    public final Attribute<String> pendingCondition =
            Attribute.define("DealPendingCondition", String.class);
    public final ChannelMap<Map> conditionMessages =
            ChannelMap.define("DealConditionMessages", Map.class);

    private final InitializeDeal initialize = new InitializeDeal();
    private final WaitForDealCondition waitForCondition = new WaitForDealCondition();
    private final ExecuteDealAction executeAction = new ExecuteDealAction();
    private final EvaluateDealTransition evaluateTransition = new EvaluateDealTransition();

    @Override
    public StepList<DealStart> getSteps() {
        return StepList.startStep(initialize)
                .otherSteps(waitForCondition, executeAction, evaluateTransition);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                definition,
                stateData,
                processId,
                itemId,
                buyerId,
                currentState,
                pendingCondition,
                conditionMessages);
    }

    @SuppressWarnings("unchecked")
    private Map<String, String> readStateData(final Context context) {
        return new LinkedHashMap<>((Map<String, String>) stateData.get(context));
    }

    @SuppressWarnings("unchecked")
    private void mergeCondition(final Context context, final String conditionName) {
        final List<Map> messages = conditionMessages.getConditionResults(context, conditionName);
        if (messages.size() != 1) {
            throw new IllegalStateException("condition " + conditionName + " requires one message");
        }
        final Map<String, String> merged = readStateData(context);
        merged.putAll((Map<String, String>) messages.get(0));
        stateData.set(context, merged);
    }

    private void runAction(final Context context, final String actionName) {
        final Map<String, String> updated = readStateData(context);
        if ("deliverItemToBuyer".equals(actionName)) {
            updated.put("itemDeliveryStatus", "delivered");
        } else if (!"chargeBuyer".equals(actionName)) {
            throw new IllegalArgumentException("deal action " + actionName + " is not registered");
        }
        updated.put("lastAction", actionName);
        stateData.set(context, updated);
    }

    final class InitializeDeal implements Step<DealStart> {
        @Override
        public Class<DealStart> getInputType() {
            return DealStart.class;
        }

        @Override
        public StepDecision execute(final Context context, final DealStart input) {
            input.definition.state(input.definition.initialState);
            definition.set(context, input.definition);
            processId.set(context, input.definition.processId);
            itemId.set(context, input.definition.itemId);
            buyerId.set(context, input.buyerId);
            stateData.set(context, new LinkedHashMap<>(input.definition.initialStateData));
            return StepDecision.goTo(
                    WaitForDealCondition.class,
                    new StateStepInput(input.definition.initialState));
        }
    }

    final class WaitForDealCondition implements Step<StateStepInput> {
        @Override
        public Class<StateStepInput> getInputType() {
            return StateStepInput.class;
        }

        @Override
        public Wait waitFor(final Context context, final StateStepInput input) {
            final DealDefinition.DealState state = definition.get(context).state(input.stateName);
            if (state.preCondition == null) {
                return Wait.skipImmediately();
            }
            pendingCondition.set(context, state.preCondition.name);
            return Wait.until(conditionMessages.forOne(state.preCondition.name));
        }

        @Override
        public StepDecision execute(final Context context, final StateStepInput input) {
            final DealDefinition.DealState state = definition.get(context).state(input.stateName);
            if (state.preCondition != null) {
                mergeCondition(context, state.preCondition.name);
                pendingCondition.delete(context);
            }
            currentState.set(context, state.name);
            if (!state.actions.isEmpty()) {
                return StepDecision.goTo(
                        ExecuteDealAction.class,
                        new ActionStepInput(state.name, 0));
            }
            return StepDecision.goTo(EvaluateDealTransition.class, input);
        }
    }

    final class ExecuteDealAction implements Step<ActionStepInput> {
        @Override
        public Class<ActionStepInput> getInputType() {
            return ActionStepInput.class;
        }

        @Override
        public StepDecision execute(final Context context, final ActionStepInput input) {
            final DealDefinition.DealState state = definition.get(context).state(input.stateName);
            if (input.actionIndex < 0 || input.actionIndex >= state.actions.size()) {
                throw new IllegalArgumentException("invalid action index " + input.actionIndex);
            }
            runAction(context, state.actions.get(input.actionIndex));
            final int nextIndex = input.actionIndex + 1;
            if (nextIndex < state.actions.size()) {
                return StepDecision.goTo(
                        ExecuteDealAction.class,
                        new ActionStepInput(state.name, nextIndex));
            }
            return StepDecision.goTo(
                    EvaluateDealTransition.class,
                    new StateStepInput(state.name));
        }
    }

    final class EvaluateDealTransition implements Step<StateStepInput> {
        @Override
        public Class<StateStepInput> getInputType() {
            return StateStepInput.class;
        }

        @Override
        public Wait waitFor(final Context context, final StateStepInput input) {
            final DealDefinition.DealTransition transition =
                    definition.get(context).state(input.stateName).transition;
            if (transition == null || transition.waitFor == null) {
                return Wait.skipImmediately();
            }
            pendingCondition.set(context, transition.waitFor.name);
            return Wait.until(conditionMessages.forOne(transition.waitFor.name));
        }

        @Override
        public StepDecision execute(final Context context, final StateStepInput input) {
            final DealDefinition.DealTransition transition =
                    definition.get(context).state(input.stateName).transition;
            if (transition == null) {
                return StepDecision.gracefulComplete(readStateData(context));
            }
            if (transition.waitFor != null) {
                mergeCondition(context, transition.waitFor.name);
                pendingCondition.delete(context);
            }
            final String value = readStateData(context).get(transition.key);
            final String nextState = transition.cases.stream()
                    .filter(dealCase -> dealCase.equals.equals(value))
                    .map(dealCase -> dealCase.goToState)
                    .findFirst()
                    .orElse(transition.elseState);
            return StepDecision.goTo(
                    WaitForDealCondition.class,
                    new StateStepInput(nextState));
        }
    }

    public static class StateStepInput {
        public String stateName;

        public StateStepInput() {
        }

        StateStepInput(final String stateName) {
            this.stateName = stateName;
        }
    }

    public static class ActionStepInput {
        public String stateName;
        public int actionIndex;

        public ActionStepInput() {
        }

        ActionStepInput(final String stateName, final int actionIndex) {
            this.stateName = stateName;
            this.actionIndex = actionIndex;
        }
    }
}
