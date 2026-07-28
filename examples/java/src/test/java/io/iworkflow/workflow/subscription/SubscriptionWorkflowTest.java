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
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.command.CommandRequest;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.command.TimerCommand;
import io.iworkflow.core.communication.Communication;
import io.iworkflow.core.communication.SignalCommand;
import io.iworkflow.core.persistence.Persistence;
import io.iworkflow.workflow.MyDependencyService;
import io.iworkflow.workflow.subscription.model.Customer;
import io.iworkflow.workflow.subscription.model.ImmutableCustomer;
import io.iworkflow.workflow.subscription.model.ImmutableSubscription;
import io.iworkflow.workflow.subscription.model.Subscription;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.Mock;
import org.mockito.junit.jupiter.MockitoExtension;

import java.time.Duration;

import static io.iworkflow.workflow.subscription.ChargeCurrentBillState.subscriptionOverKey;
import static io.iworkflow.workflow.subscription.SubscriptionWorkflow.keyBillingPeriodNum;
import static io.iworkflow.workflow.subscription.SubscriptionWorkflow.keyCustomer;
import static io.iworkflow.workflow.subscription.SubscriptionWorkflow.signalCancelSubscription;
import static io.iworkflow.workflow.subscription.SubscriptionWorkflow.signalUpdateBillingPeriodCharge;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

@ExtendWith(MockitoExtension.class)
public class SubscriptionWorkflowTest {
    @Mock
    private MyDependencyService myService;

    @Mock
    private Context context;
    @Mock
    private Persistence persistence;
    @Mock
    private Communication communication;
    @Mock
    private CommandResults commandResults;

    private Customer testCustomer = ImmutableCustomer.builder()
            .id("123")
            .email("qlong.seattle@gmail.com")
            .lastName("Long")
            .firstName("Quanzheng")
            .subscription(
                    ImmutableSubscription.builder()
                            .billingPeriodCharge(100)
                            .maxBillingPeriods(10)
                            .trialPeriod(Duration.ofSeconds(2))
                            .billingPeriod(Duration.ofSeconds(1))
                            .build()
            )
            .build();

    private InitState initState;
    private CancelState cancelState;
    private ChargeCurrentBillState chargeCurrentBillState;
    private TrialState trialState;

    private UpdateChargeAmountState updateChargeAmountState;

    @BeforeEach
    void beforeEach() {
        cancelState = new CancelState(myService);
        chargeCurrentBillState = new ChargeCurrentBillState(myService);
        trialState = new TrialState(myService);
        initState = new InitState();
        updateChargeAmountState = new UpdateChargeAmountState();
    }

    @Test
    public void testInitStateStart() {
        final CommandRequest commandRequest = initState.waitUntil(context, testCustomer, persistence, communication);

        assertEquals(CommandRequest.empty, commandRequest);
        verify(persistence).setDataAttribute(keyCustomer, testCustomer);
    }

    @Test
    public void testInitStateDecide() {
        final StateDecision decision = initState.execute(context, null, commandResults, persistence, communication);

        assertEquals(StateDecision.multiNextStates(TrialState.class, CancelState.class, UpdateChargeAmountState.class), decision);
    }

    @Test
    public void testTrialStateStart() {
        when(persistence.getDataAttribute(keyCustomer, Customer.class)).thenReturn(testCustomer);
        final CommandRequest commandRequest = trialState.waitUntil(context, null, persistence, communication);

        assertEquals(CommandRequest.forAllCommandCompleted(
                        TimerCommand.createByDuration(testCustomer.getSubscription().getTrialPeriod())
                ),
                commandRequest);
        verify(persistence).getDataAttribute(keyCustomer, Customer.class);
    }

    @Test
    public void testTrialStateDecide() {
        final StateDecision decision = trialState.execute(context, null, commandResults, persistence, communication);

        assertEquals(StateDecision.singleNextState(ChargeCurrentBillState.class), decision);
        verify(persistence).setDataAttribute(keyBillingPeriodNum, 0);
    }

    @Test
    public void testChargeCurrentBillStateStart_waitForDuration() {
        when(persistence.getDataAttribute(keyCustomer, Customer.class)).thenReturn(testCustomer);
        when(persistence.getDataAttribute(keyBillingPeriodNum, Integer.class)).thenReturn(0);

        final CommandRequest commandRequest = chargeCurrentBillState.waitUntil(context, null, persistence, communication);
        assertEquals(CommandRequest.forAllCommandCompleted(
                TimerCommand.createByDuration(testCustomer.getSubscription().getBillingPeriod())
        ), commandRequest);
        verify(persistence).getDataAttribute(keyCustomer, Customer.class);
        verify(persistence).getDataAttribute(keyBillingPeriodNum, Integer.class);
        verify(persistence).setDataAttribute(keyBillingPeriodNum, 1);
    }

    @Test
    public void testChargeCurrentBillStateStart_subscriptionOver() {
        when(persistence.getDataAttribute(keyCustomer, Customer.class)).thenReturn(testCustomer);
        when(persistence.getDataAttribute(keyBillingPeriodNum, Integer.class)).thenReturn(testCustomer.getSubscription().getMaxBillingPeriods());

        final CommandRequest commandRequest = chargeCurrentBillState.waitUntil(context, null, persistence, communication);
        assertEquals(CommandRequest.empty, commandRequest);

        verify(persistence).getDataAttribute(keyCustomer, Customer.class);
        verify(persistence).getDataAttribute(keyBillingPeriodNum, Integer.class);
        verify(persistence).setStateExecutionLocal(subscriptionOverKey, true);
    }

    @Test
    public void testChargeCurrentBillStateDecide_subscriptionNotOver() {
        when(persistence.getDataAttribute(keyCustomer, Customer.class)).thenReturn(testCustomer);
        when(persistence.getStateExecutionLocal(subscriptionOverKey, Boolean.class)).thenReturn(null);

        final StateDecision decision = chargeCurrentBillState.execute(context, null, commandResults, persistence, communication);
        assertEquals(StateDecision.singleNextState(ChargeCurrentBillState.class), decision);

        verify(persistence).getDataAttribute(keyCustomer, Customer.class);
        verify(persistence).getStateExecutionLocal(subscriptionOverKey, Boolean.class);
        verify(myService).chargeUser(testCustomer.getEmail(), testCustomer.getId(), testCustomer.getSubscription().getBillingPeriodCharge());
    }

    @Test
    public void testChargeCurrentBillStateDecide_subscriptionOver() {
        when(persistence.getDataAttribute(keyCustomer, Customer.class)).thenReturn(testCustomer);
        when(persistence.getStateExecutionLocal(subscriptionOverKey, Boolean.class)).thenReturn(true);

        final StateDecision decision = chargeCurrentBillState.execute(context, null, commandResults, persistence, communication);
        assertEquals(StateDecision.forceCompleteWorkflow(), decision);

        verify(persistence).getDataAttribute(keyCustomer, Customer.class);
        verify(persistence).getStateExecutionLocal(subscriptionOverKey, Boolean.class);
        verify(myService).sendEmail(any(), any(), any());
    }

    @Test
    public void testUpdateChargeAmountStateStart() {
        final CommandRequest commandRequest = updateChargeAmountState.waitUntil(context, null, persistence, communication);

        assertEquals(CommandRequest.forAllCommandCompleted(
                        SignalCommand.create(signalUpdateBillingPeriodCharge)
                ),
                commandRequest);
    }

    @Test
    public void testUpdateChargeAmountStateDecide() {
        when(persistence.getDataAttribute(keyCustomer, Customer.class)).thenReturn(testCustomer);
        when(commandResults.getSignalValueByIndex(0)).thenReturn(200);

        final StateDecision decision = updateChargeAmountState.execute(context, null, commandResults, persistence, communication);

        assertEquals(StateDecision.singleNextState(UpdateChargeAmountState.class), decision);

        final Subscription updatedSubscription = ImmutableSubscription.builder().from(testCustomer.getSubscription()).billingPeriodCharge(200).build();
        final Customer updatedCustomer = ImmutableCustomer.copyOf(testCustomer)
                .withSubscription(updatedSubscription);

        verify(persistence).setDataAttribute(keyCustomer, updatedCustomer);
        verify(persistence).getDataAttribute(keyCustomer, Customer.class);
        verify(commandResults).getSignalValueByIndex(0);
    }

    @Test
    public void testCancelStateStart() {
        final CommandRequest commandRequest = cancelState.waitUntil(context, null, persistence, communication);

        assertEquals(CommandRequest.forAllCommandCompleted(
                        SignalCommand.create(signalCancelSubscription)
                ),
                commandRequest);
    }

    @Test
    public void testCancelStateDecide() {
        when(persistence.getDataAttribute(keyCustomer, Customer.class)).thenReturn(testCustomer);
        final StateDecision decision = cancelState.execute(context, null, commandResults, persistence, communication);

        assertEquals(StateDecision.forceCompleteWorkflow(), decision);

        verify(persistence).getDataAttribute(keyCustomer, Customer.class);
    }
}
