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

package io.superdurable.dex.patterns.workflowpatterns;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeLock;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

/** WCP-40 Interleaved Routing: audits run in any order, one at a time, then one join fires. */
@Component
public class InterleavedRoutingFlow implements Flow<Void> {
    public static final Attribute<Boolean> auditLock = Attribute.define("auditLock", Boolean.class);
    public static final Channel<TaskInterval> auditFinished =
            Channel.define("auditFinished", TaskInterval.class);
    private static final int AUDIT_COUNT = 3;
    private static final long AUDIT_MILLIS = 200L;

    private final StartAudits startAudits = new StartAudits();
    private final AuditPayroll auditPayroll = new AuditPayroll();
    private final AuditExpenses auditExpenses = new AuditExpenses();
    private final AuditTaxes auditTaxes = new AuditTaxes();
    private final CompleteAudits completeAudits = new CompleteAudits();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(startAudits)
                .otherSteps(auditPayroll, auditExpenses, auditTaxes, completeAudits);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(auditLock, auditFinished);
    }

    private static StepOptions auditLockOptions() {
        return StepOptions.newBuilder().addExecuteLock(AttributeLock.of(auditLock)).build();
    }

    private static StepDecision runAudit(final Context context, final String audit) {
        final long startedAtMillis = System.currentTimeMillis();
        TaskInterval.sleepInsideTask(AUDIT_MILLIS);
        auditFinished.publish(context, TaskInterval.endingNow(audit, startedAtMillis));
        return StepDecision.deadEnd();
    }

    static final class StartAudits implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goToMany(
                    StepMovement.of(AuditPayroll.class, null),
                    StepMovement.of(AuditExpenses.class, null),
                    StepMovement.of(AuditTaxes.class, null),
                    StepMovement.of(CompleteAudits.class, AUDIT_COUNT));
        }
    }

    static final class AuditPayroll implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return auditLockOptions();
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return runAudit(context, getStepType());
        }
    }

    static final class AuditExpenses implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return auditLockOptions();
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return runAudit(context, getStepType());
        }
    }

    static final class AuditTaxes implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return auditLockOptions();
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return runAudit(context, getStepType());
        }
    }

    static final class CompleteAudits implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer auditCount) {
            return Wait.until(auditFinished.forN(auditCount));
        }

        @Override
        public StepDecision execute(final Context context, final Integer auditCount) {
            return StepDecision.gracefulComplete(auditFinished.getConditionResults(context));
        }
    }
}
