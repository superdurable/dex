# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from dataclasses import dataclass, field

from dex import (
    Attribute,
    ChannelMap,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Wait,
    go_to,
    graceful_complete,
)


@dataclass(frozen=True)
class DealCondition:
    name: str


@dataclass(frozen=True)
class DealCase:
    equals: str
    go_to_state: str


@dataclass(frozen=True)
class DealTransition:
    else_state: str
    wait_for: DealCondition | None = None
    key: str = ""
    cases: tuple[DealCase, ...] = ()


@dataclass(frozen=True)
class DealState:
    name: str
    pre_condition: DealCondition | None = None
    actions: tuple[str, ...] = ()
    transition: DealTransition | None = None


@dataclass(frozen=True)
class DealDefinition:
    process_id: str
    item_id: str
    item_name: str
    initial_state: str
    initial_state_data: dict[str, str] = field(default_factory=dict)
    states: tuple[DealState, ...] = ()

    def state(self, name: str) -> DealState:
        for state in self.states:
            if state.name == name:
                return normalize_state(state)
        raise ValueError(f"deal state {name!r} is not defined")


def normalize_state(state: DealState) -> DealState:
    return DealState(
        name=state.name,
        pre_condition=normalize_condition(state.pre_condition),
        actions=tuple(state.actions),
        transition=normalize_transition(state.transition),
    )


def normalize_condition(
    condition: DealCondition | dict[str, str] | None,
) -> DealCondition | None:
    if condition is None or isinstance(condition, DealCondition):
        return condition
    return DealCondition(name=condition["name"])


def normalize_transition(
    transition: DealTransition | dict[str, object] | None,
) -> DealTransition | None:
    if transition is None:
        return None
    if isinstance(transition, DealTransition):
        return DealTransition(
            else_state=transition.else_state,
            wait_for=normalize_condition(transition.wait_for),
            key=transition.key,
            cases=tuple(normalize_case(deal_case) for deal_case in transition.cases),
        )
    wait_for = transition.get("wait_for")
    if wait_for is not None and not isinstance(wait_for, dict):
        raise ValueError("deal transition wait_for must be an object")
    cases = transition.get("cases", ())
    if not isinstance(cases, (list, tuple)):
        raise ValueError("deal transition cases must be a list")
    return DealTransition(
        else_state=str(transition["else_state"]),
        wait_for=normalize_condition(wait_for),
        key=str(transition.get("key", "")),
        cases=tuple(normalize_case(deal_case) for deal_case in cases),
    )


def normalize_case(deal_case: DealCase | dict[str, str]) -> DealCase:
    if isinstance(deal_case, DealCase):
        return deal_case
    return DealCase(
        equals=deal_case["equals"],
        go_to_state=deal_case["go_to_state"],
    )


@dataclass(frozen=True)
class DealStart:
    definition: DealDefinition
    buyer_id: str


@dataclass(frozen=True)
class StateStepInput:
    state_name: str


@dataclass(frozen=True)
class ActionStepInput:
    state_name: str
    action_index: int


class InitializeDeal(Step[DealStart]):
    def __init__(self, flow: "DealDSLFlow") -> None:
        self.flow = flow

    def execute(self, context: Context, input: DealStart) -> StepDecision:
        definition = input.definition
        definition.state(definition.initial_state)
        self.flow.definition.set(context, definition)
        self.flow.process_id.set(context, definition.process_id)
        self.flow.item_id.set(context, definition.item_id)
        self.flow.buyer_id.set(context, input.buyer_id)
        self.flow.state_data.set(context, dict(definition.initial_state_data))
        return go_to(WaitForDealCondition, StateStepInput(definition.initial_state))


class WaitForDealCondition(Step[StateStepInput]):
    def __init__(self, flow: "DealDSLFlow") -> None:
        self.flow = flow

    def wait_for(self, context: Context, input: StateStepInput) -> Wait:
        state = self.flow.definition.get(context).state(input.state_name)
        if state.pre_condition is None:
            return Wait.skip_immediately()
        self.flow.pending_condition.set(context, state.pre_condition.name)
        return Wait.until(self.flow.condition_messages.for_one(state.pre_condition.name))

    def execute(self, context: Context, input: StateStepInput) -> StepDecision:
        state = self.flow.definition.get(context).state(input.state_name)
        if state.pre_condition is not None:
            self.flow.merge_condition(context, state.pre_condition.name)
            self.flow.pending_condition.delete(context)
        self.flow.current_state.set(context, state.name)
        if state.actions:
            return go_to(ExecuteDealAction, ActionStepInput(state.name, 0))
        return go_to(EvaluateDealTransition, input)


class ExecuteDealAction(Step[ActionStepInput]):
    def __init__(self, flow: "DealDSLFlow") -> None:
        self.flow = flow

    def execute(self, context: Context, input: ActionStepInput) -> StepDecision:
        state = self.flow.definition.get(context).state(input.state_name)
        if input.action_index < 0 or input.action_index >= len(state.actions):
            raise ValueError(f"invalid action index {input.action_index}")
        self.flow.execute_action(context, state.actions[input.action_index])
        next_index = input.action_index + 1
        if next_index < len(state.actions):
            return go_to(ExecuteDealAction, ActionStepInput(state.name, next_index))
        return go_to(EvaluateDealTransition, StateStepInput(state.name))


class EvaluateDealTransition(Step[StateStepInput]):
    def __init__(self, flow: "DealDSLFlow") -> None:
        self.flow = flow

    def wait_for(self, context: Context, input: StateStepInput) -> Wait:
        transition = self.flow.definition.get(context).state(input.state_name).transition
        if transition is None or transition.wait_for is None:
            return Wait.skip_immediately()
        self.flow.pending_condition.set(context, transition.wait_for.name)
        return Wait.until(self.flow.condition_messages.for_one(transition.wait_for.name))

    def execute(self, context: Context, input: StateStepInput) -> StepDecision:
        transition = self.flow.definition.get(context).state(input.state_name).transition
        if transition is None:
            return graceful_complete(self.flow.state_data.get(context))
        if transition.wait_for is not None:
            self.flow.merge_condition(context, transition.wait_for.name)
            self.flow.pending_condition.delete(context)
        state_data = self.flow.state_data.get(context)
        next_state = transition.else_state
        for deal_case in transition.cases:
            if state_data.get(transition.key) == deal_case.equals:
                next_state = deal_case.go_to_state
                break
        return go_to(WaitForDealCondition, StateStepInput(next_state))


class DealDSLFlow(Flow[DealStart]):
    definition = Attribute("DealDefinition", DealDefinition)
    state_data = Attribute("DealStateData", dict[str, str])
    process_id = Attribute("DealProcessID", str)
    item_id = Attribute("DealItemID", str)
    buyer_id = Attribute("DealBuyerID", str)
    current_state = Attribute("DealCurrentState", str)
    pending_condition = Attribute("DealPendingCondition", str)
    condition_messages = ChannelMap("DealConditionMessages", dict[str, str])

    def __init__(self) -> None:
        self.initialize = InitializeDeal(self)
        self.wait_for_condition = WaitForDealCondition(self)
        self.execute_action_step = ExecuteDealAction(self)
        self.evaluate_transition = EvaluateDealTransition(self)

    def get_steps(self) -> StepList[DealStart]:
        return StepList.start_step(self.initialize).other_steps(
            self.wait_for_condition,
            self.execute_action_step,
            self.evaluate_transition,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.definition,
            self.state_data,
            self.process_id,
            self.item_id,
            self.buyer_id,
            self.current_state,
            self.pending_condition,
            self.condition_messages,
        )

    def merge_condition(self, context: Context, condition_name: str) -> None:
        messages = self.condition_messages.results(context, condition_name)
        if len(messages) != 1:
            raise ValueError(f"condition {condition_name!r} requires one message")
        state_data = dict(self.state_data.get(context))
        state_data.update(messages[0])
        self.state_data.set(context, state_data)

    def execute_action(self, context: Context, action_name: str) -> None:
        state_data = dict(self.state_data.get(context))
        if action_name == "deliverItemToBuyer":
            state_data["itemDeliveryStatus"] = "delivered"
        elif action_name != "chargeBuyer":
            raise ValueError(f"deal action {action_name!r} is not registered")
        state_data["lastAction"] = action_name
        self.state_data.set(context, state_data)


def example_deal_start(buyer_id: str) -> DealStart:
    return DealStart(
        DealDefinition(
            process_id="item-deal-v1",
            item_id="item-42",
            item_name="Any sellable item",
            initial_state="negotiating",
            initial_state_data={"accepted": "false"},
            states=(
                DealState(
                    name="negotiating",
                    transition=DealTransition(
                        wait_for=DealCondition("buyer-decision"),
                        key="accepted",
                        cases=(DealCase("true", "fulfill"),),
                        else_state="declined",
                    ),
                ),
                DealState(name="fulfill", actions=("chargeBuyer", "deliverItemToBuyer")),
                DealState(name="declined"),
            ),
        ),
        buyer_id,
    )
