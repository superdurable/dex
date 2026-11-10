/*
 * Copyright (c) 2026 Super Durable, Inc.
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

// Termination Workflow Patterns: WCP-11 (implicit termination) and WCP-43
// (explicit termination).

import {
  Channel,
  StepList,
  StepMovement,
  Wait,
  forceComplete,
  goToMany,
  gracefulComplete,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

// WCP-11 Implicit Termination: GracefulComplete closes the Flow only after the
// last active Step finishes.

const profileCompleted = new Channel("profileCompleted", stringCodec);

class StartOnboarding implements Step<void> {
  public getStepType(): string {
    return "StartOnboarding";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goToMany(
      StepMovement.of(CreateAccount, undefined),
      StepMovement.of(SendWelcomeEmail, undefined),
      StepMovement.of(AwaitProfileCompletion, undefined),
    );
  }
}

class CreateAccount implements Step<void> {
  public getStepType(): string {
    return "CreateAccount";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("CreateAccount");
  }
}

class SendWelcomeEmail implements Step<void> {
  public getStepType(): string {
    return "SendWelcomeEmail";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("SendWelcomeEmail");
  }
}

class AwaitProfileCompletion implements Step<void> {
  public getStepType(): string {
    return "AwaitProfileCompletion";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(profileCompleted.forOne());
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("AwaitProfileCompletion");
  }
}

export class ImplicitTerminationFlow implements Flow<void> {
  private readonly startOnboarding = new StartOnboarding();
  private readonly createAccount = new CreateAccount();
  private readonly sendWelcomeEmail = new SendWelcomeEmail();
  private readonly awaitProfileCompletion = new AwaitProfileCompletion();

  public getFlowType(): string {
    return "ImplicitTerminationFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.startOnboarding).otherSteps(
      this.createAccount,
      this.sendWelcomeEmail,
      this.awaitProfileCompletion,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [profileCompleted] };
  }

  @rpc()
  public completeProfile(context: Context): void {
    profileCompleted.publish(context, "completed");
  }
}

// WCP-43 Explicit Termination: ForceComplete closes the Flow at once and
// cancels the Steps still waiting.

const diagnosticsCollected = new Channel("diagnosticsCollected", stringCodec);
const onCallAcknowledged = new Channel("onCallAcknowledged", stringCodec);
const incidentResolved = new Channel("incidentResolved", stringCodec);

class StartIncident implements Step<void> {
  public getStepType(): string {
    return "StartIncident";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goToMany(
      StepMovement.of(CollectDiagnostics, undefined),
      StepMovement.of(NotifyOnCall, undefined),
      StepMovement.of(ResolveIncident, undefined),
    );
  }
}

class CollectDiagnostics implements Step<void> {
  public getStepType(): string {
    return "CollectDiagnostics";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(diagnosticsCollected.forOne());
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("CollectDiagnostics");
  }
}

class NotifyOnCall implements Step<void> {
  public getStepType(): string {
    return "NotifyOnCall";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(onCallAcknowledged.forOne());
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("NotifyOnCall");
  }
}

class ResolveIncident implements Step<void> {
  public getStepType(): string {
    return "ResolveIncident";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(incidentResolved.forOne());
  }

  public execute(_context: Context, _input: void): StepDecision {
    return forceComplete("ResolveIncident");
  }
}

export class ExplicitTerminationFlow implements Flow<void> {
  private readonly startIncident = new StartIncident();
  private readonly collectDiagnostics = new CollectDiagnostics();
  private readonly notifyOnCall = new NotifyOnCall();
  private readonly resolveIncidentStep = new ResolveIncident();

  public getFlowType(): string {
    return "ExplicitTerminationFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.startIncident).otherSteps(
      this.collectDiagnostics,
      this.notifyOnCall,
      this.resolveIncidentStep,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [diagnosticsCollected, onCallAcknowledged, incidentResolved] };
  }

  @rpc()
  public resolveIncident(context: Context): void {
    incidentResolved.publish(context, "resolved");
  }
}

export const implicitTerminationFlow = new ImplicitTerminationFlow();
export const explicitTerminationFlow = new ExplicitTerminationFlow();
