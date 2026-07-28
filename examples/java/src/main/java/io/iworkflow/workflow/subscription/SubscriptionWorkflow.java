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

package io.iworkflow.workflow.subscription;

import io.iworkflow.core.Context;
import io.iworkflow.core.ObjectWorkflow;
import io.iworkflow.core.RPC;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.StateDef;
import io.iworkflow.core.WorkflowState;
import io.iworkflow.core.command.CommandRequest;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.command.TimerCommand;
import io.iworkflow.core.communication.Communication;
import io.iworkflow.core.communication.CommunicationMethodDef;
import io.iworkflow.core.communication.SignalChannelDef;
import io.iworkflow.core.communication.SignalCommand;
import io.iworkflow.core.persistence.DataAttributeDef;
import io.iworkflow.core.persistence.Persistence;
import io.iworkflow.core.persistence.PersistenceFieldDef;
import io.iworkflow.workflow.MyDependencyService;
import io.iworkflow.workflow.subscription.model.Customer;
import io.iworkflow.workflow.subscription.model.ImmutableCustomer;
import io.iworkflow.workflow.subscription.model.ImmutableSubscription;
import io.iworkflow.workflow.subscription.model.Subscription;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

import static io.iworkflow.workflow.subscription.SubscriptionWorkflow.keyBillingPeriodNum;
import static io.iworkflow.workflow.subscription.SubscriptionWorkflow.keyCustomer;
import static io.iworkflow.workflow.subscription.SubscriptionWorkflow.signalCancelSubscription;
import static io.iworkflow.workflow.subscription.SubscriptionWorkflow.signalUpdateBillingPeriodCharge;

@Component
public class SubscriptionWorkflow implements ObjectWorkflow {

    @Autowired
    private MyDependencyService myService;

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new InitState()),
                StateDef.nonStartingState(new CancelState(myService)),
                StateDef.nonStartingState(new ChargeCurrentBillState(myService)),
                StateDef.nonStartingState(new TrialState(myService)),
                StateDef.nonStartingState(new UpdateChargeAmountState())

        );
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(Customer.class, keyCustomer),
                DataAttributeDef.create(Integer.class, keyBillingPeriodNum)
        );
    }

    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                SignalChannelDef.create(Void.class, signalCancelSubscription),
                SignalChannelDef.create(Integer.class, signalUpdateBillingPeriodCharge)
        );
    }

    @RPC
    public Subscription describe(Context context, Persistence persistence, Communication communication) {
        // Note that a readOnly RPC will not write any event to history
        final Customer customer = persistence.getDataAttribute(keyCustomer, Customer.class);
        return customer.getSubscription();
    }

    public static final String keyBillingPeriodNum = "billingPeriodNum";
    public static final String keyCustomer = "customer";

    public static final String signalCancelSubscription = "cancelSubscription";
    public static final String signalUpdateBillingPeriodCharge = "updateBillingPeriodCharge";
}

class InitState implements WorkflowState<Customer> {

    @Override
    public Class<Customer> getInputType() {
        return Customer.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final Customer input, Persistence persistence, final Communication communication) {
        persistence.setDataAttribute(keyCustomer, input);
        return CommandRequest.empty;
    }

    @Override
    public StateDecision execute(final Context context, final Customer input, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        return StateDecision.multiNextStates(TrialState.class, CancelState.class, UpdateChargeAmountState.class);
    }
}

class TrialState implements WorkflowState<Void> {

    private MyDependencyService myService;

    TrialState(MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final Void input, Persistence persistence, final Communication communication) {
        final Customer customer = persistence.getDataAttribute(keyCustomer, Customer.class);
        myService.sendEmail(customer.getEmail(), "welcome to triage", "Hello, this is the content of the email...");

        return CommandRequest.forAllCommandCompleted(
                TimerCommand.createByDuration(customer.getSubscription().getTrialPeriod())
        );
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        persistence.setDataAttribute(keyBillingPeriodNum, 0);
        return StateDecision.singleNextState(ChargeCurrentBillState.class);
    }
}

class ChargeCurrentBillState implements WorkflowState<Void> {

    private MyDependencyService myService;

    ChargeCurrentBillState(MyDependencyService myService) {
        this.myService = myService;
    }

    final static String subscriptionOverKey = "subscriptionOver";

    @Override

    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final Void input, Persistence persistence, final Communication communication) {
        final Customer customer = persistence.getDataAttribute(keyCustomer, Customer.class);
        final int periodNum = persistence.getDataAttribute(keyBillingPeriodNum, Integer.class);

        // if customer.getSubscription().getMaxBillingPeriods() == 0, it's unlimited subscription periods
        if (customer.getSubscription().getMaxBillingPeriods() > 0 && periodNum >= customer.getSubscription().getMaxBillingPeriods()) {
            persistence.setStateExecutionLocal(subscriptionOverKey, true);
            return CommandRequest.empty;
        }

        persistence.setDataAttribute(keyBillingPeriodNum, periodNum + 1);
        return CommandRequest.forAllCommandCompleted(
                TimerCommand.createByDuration(customer.getSubscription().getBillingPeriod())
        );
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        final Customer customer = persistence.getDataAttribute(keyCustomer, Customer.class);

        final Boolean subscriptionOver = persistence.getStateExecutionLocal(subscriptionOverKey, Boolean.class);
        if (subscriptionOver != null && subscriptionOver) {
            myService.sendEmail(customer.getEmail(), "subscription over", "Hello, this is the content of the email...");
            return StateDecision.forceCompleteWorkflow();
        }

        myService.chargeUser(customer.getEmail(), customer.getId(), customer.getSubscription().getBillingPeriodCharge());

        return StateDecision.singleNextState(ChargeCurrentBillState.class);
    }
}

class UpdateChargeAmountState implements WorkflowState<Void> {

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final Void input, Persistence persistence, final Communication communication) {
        return CommandRequest.forAllCommandCompleted(
                SignalCommand.create(signalUpdateBillingPeriodCharge)
        );
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        final Customer customer = persistence.getDataAttribute(keyCustomer, Customer.class);

        final int newAmount = commandResults.getSignalValueByIndex(0);

        final Subscription updatedSubscription = ImmutableSubscription.builder().from(customer.getSubscription()).billingPeriodCharge(newAmount).build();
        final Customer updatedCustomer = ImmutableCustomer.copyOf(customer)
                .withSubscription(updatedSubscription);
        persistence.setDataAttribute(keyCustomer, updatedCustomer);

        return StateDecision.singleNextState(UpdateChargeAmountState.class);
    }
}

class CancelState implements WorkflowState<Void> {

    private MyDependencyService myService;

    CancelState(final MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final Void input, Persistence persistence, final Communication communication) {
        return CommandRequest.forAllCommandCompleted(
                SignalCommand.create(signalCancelSubscription)
        );
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        final Customer customer = persistence.getDataAttribute(keyCustomer, Customer.class);

        myService.sendEmail(customer.getEmail(), "subscription canceled", "Hello, this is the content of the email...");

        return StateDecision.forceCompleteWorkflow();
    }
}