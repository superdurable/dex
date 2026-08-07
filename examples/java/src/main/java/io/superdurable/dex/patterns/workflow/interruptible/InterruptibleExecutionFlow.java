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

package io.superdurable.dex.patterns.workflow.interruptible;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class InterruptibleExecutionFlow implements Flow<Void> {
    public static final String DA_INTERRUPT_SIGNAL = "interruptSignal";

    public final Attribute<String> interruptSignal =
            Attribute.define(DA_INTERRUPT_SIGNAL, String.class);

    private final Init init = new Init();
    private final WorkAExecution workAExecution = new WorkAExecution();
    private final WorkNExecution workNExecution = new WorkNExecution();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(init).otherSteps(workAExecution, workNExecution);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(interruptSignal);
    }

    @RPC
    public void interrupt(final Context context) {
        interruptSignal.set(context, "cancel");
    }

    final class Init implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void unused) {
            final WorkJobParametersInput input = new WorkJobParametersInput(15, 1);
            return StepDecision.goToMulti(
                    StepMovement.of(workAExecution, input),
                    StepMovement.of(workNExecution, input));
        }
    }

    final class WorkAExecution implements Step<WorkJobParametersInput> {
        @Override
        public Class<WorkJobParametersInput> getInputType() {
            return WorkJobParametersInput.class;
        }

        @Override
        public Wait waitFor(final Context context, final WorkJobParametersInput input) {
            return Wait.anyOf(Timer.byDuration(Duration.ofMillis(1500)));
        }

        @Override
        public StepDecision execute(
                final Context context,
                final WorkJobParametersInput input) {
            final String signal = interruptSignal.get(context);
            if (signal != null && signal.equals("cancel")) {
                System.out.println("A: Interrupted!");
                return StepDecision.gracefulComplete();
            }

            if (input.progress > input.jobUpperBound) {
                System.out.println("Executing WorkAExecution completed");
                return StepDecision.gracefulComplete();
            }
            System.out.printf(
                    "[%s][%s]: Doing job %s%n",
                    context.getFlowId(),
                    context.getStepExecutionId(),
                    input.progress);

            final WorkJobParametersInput next =
                    new WorkJobParametersInput(input.jobUpperBound, input.progress + 1);
            return StepDecision.goTo(workAExecution, next);
        }
    }

    final class WorkNExecution implements Step<WorkJobParametersInput> {
        @Override
        public Class<WorkJobParametersInput> getInputType() {
            return WorkJobParametersInput.class;
        }

        @Override
        public Wait waitFor(final Context context, final WorkJobParametersInput input) {
            return Wait.anyOf(Timer.byDuration(Duration.ofSeconds(3)));
        }

        @Override
        public StepDecision execute(
                final Context context,
                final WorkJobParametersInput input) {
            final String signal = interruptSignal.get(context);
            if (signal != null && signal.equals("cancel")) {
                System.out.println("N: Interrupted!");
                return StepDecision.gracefulComplete();
            }

            if (input.progress > input.jobUpperBound) {
                System.out.println("Executing WorkNExecution completed");
                return StepDecision.gracefulComplete();
            }

            System.out.printf(
                    "[%s][%s]: Processing job %s%n",
                    context.getFlowId(),
                    context.getStepExecutionId(),
                    input.progress);

            final WorkJobParametersInput next =
                    new WorkJobParametersInput(input.jobUpperBound, input.progress + 1);
            return StepDecision.goTo(workNExecution, next);
        }
    }
}
