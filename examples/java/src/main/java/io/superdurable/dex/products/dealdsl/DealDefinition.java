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

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

public class DealDefinition {
    public String processId;
    public String itemId;
    public String itemName;
    public String initialState;
    public Map<String, String> initialStateData = new LinkedHashMap<>();
    public List<DealState> states = new ArrayList<>();

    public DealDefinition() {
    }

    public DealState state(final String name) {
        return states.stream()
                .filter(state -> state.name.equals(name))
                .findFirst()
                .orElseThrow(() -> new IllegalArgumentException(
                        "deal state " + name + " is not defined"));
    }

    public static DealDefinition example() {
        final DealDefinition definition = new DealDefinition();
        definition.processId = "item-deal-v1";
        definition.itemId = "item-42";
        definition.itemName = "Any sellable item";
        definition.initialState = "negotiating";
        definition.initialStateData.put("accepted", "false");

        final DealTransition negotiation = new DealTransition();
        negotiation.waitFor = new DealCondition("buyer-decision");
        negotiation.key = "accepted";
        negotiation.cases.add(new DealCase("true", "fulfill"));
        negotiation.elseState = "declined";
        definition.states.add(new DealState("negotiating", negotiation));
        definition.states.add(new DealState(
                "fulfill",
                List.of("chargeBuyer", "deliverItemToBuyer")));
        definition.states.add(new DealState("declined"));
        return definition;
    }

    public static class DealCondition {
        public String name;

        public DealCondition() {
        }

        public DealCondition(final String name) {
            this.name = name;
        }
    }

    public static class DealCase {
        public String equals;
        public String goToState;

        public DealCase() {
        }

        public DealCase(final String equals, final String goToState) {
            this.equals = equals;
            this.goToState = goToState;
        }
    }

    public static class DealTransition {
        public String elseState;
        public DealCondition waitFor;
        public String key = "";
        public List<DealCase> cases = new ArrayList<>();
    }

    public static class DealState {
        public String name;
        public DealCondition preCondition;
        public List<String> actions = new ArrayList<>();
        public DealTransition transition;

        public DealState() {
        }

        public DealState(final String name) {
            this.name = name;
        }

        public DealState(final String name, final DealTransition transition) {
            this.name = name;
            this.transition = transition;
        }

        public DealState(final String name, final List<String> actions) {
            this.name = name;
            this.actions = actions;
        }
    }
}
