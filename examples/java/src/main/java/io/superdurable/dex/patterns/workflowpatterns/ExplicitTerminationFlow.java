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

import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

/** WCP-43 Explicit Termination: ForceComplete closes the Flow and cancels every other Step. */
@Component
public class ExplicitTerminationFlow implements Flow<Void> {
    public static final Channel<String> diagnosticsCollected =
            Channel.define("diagnosticsCollected", String.class);
    public static final Channel<String> onCallAcknowledged =
            Channel.define("onCallAcknowledged", String.class);
    public static final Channel<String> incidentResolved =
            Channel.define("incidentResolved", String.class);

    private final StartIncident startIncident = new StartIncident();
    private final CollectDiagnostics collectDiagnostics = new CollectDiagnostics();
    private final NotifyOnCall notifyOnCall = new NotifyOnCall();
    private final ResolveIncident resolveIncident = new ResolveIncident();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(startIncident)
                .otherSteps(collectDiagnostics, notifyOnCall, resolveIncident);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(diagnosticsCollected, onCallAcknowledged, incidentResolved);
    }

    @RPC
    public void resolveIncident(final Context context) {
        incidentResolved.publish(context, "resolved");
    }

    static final class StartIncident implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goToMany(
                    StepMovement.of(CollectDiagnostics.class, null),
                    StepMovement.of(NotifyOnCall.class, null),
                    StepMovement.of(ResolveIncident.class, null));
        }
    }

    static final class CollectDiagnostics implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(diagnosticsCollected.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(getStepType());
        }
    }

    static final class NotifyOnCall implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(onCallAcknowledged.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(getStepType());
        }
    }

    static final class ResolveIncident implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(incidentResolved.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.forceComplete(getStepType());
        }
    }
}
