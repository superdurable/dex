// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package workflowpatterns

import (
	"github.com/superdurable/dex/sdk-go/dex"
)

var ProfileCompletedChannel = dex.DefineChannel[string]("profileCompleted")

// ImplicitTerminationFlow implements WCP-11 Implicit Termination: GracefulComplete
// closes the Flow only after every active Step finishes.
type ImplicitTerminationFlow struct{ dex.FlowDefaults }

func (*ImplicitTerminationFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartOnboarding{}),
		dex.DefineStep(CreateAccount{}),
		dex.DefineStep(SendWelcomeEmail{}),
		dex.DefineStep(AwaitProfileCompletion{}),
	}
}

func (flow *ImplicitTerminationFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.CompleteProfile, nil)}
}

func (*ImplicitTerminationFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{ProfileCompletedChannel}}
}

// CompleteProfile records that the user completed the profile.
func (*ImplicitTerminationFlow) CompleteProfile(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := ProfileCompletedChannel.Publish(ctx, "completed"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type StartOnboarding struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (StartOnboarding) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(CreateAccount{}, nil),
		dex.MovementOf(SendWelcomeEmail{}, nil),
		dex.MovementOf(AwaitProfileCompletion{}, nil),
	), nil
}

type CreateAccount struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (CreateAccount) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("CreateAccount"), nil
}

type SendWelcomeEmail struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (SendWelcomeEmail) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("SendWelcomeEmail"), nil
}

type AwaitProfileCompletion struct{ dex.StepDefaults }

func (AwaitProfileCompletion) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(ProfileCompletedChannel.ForOne()), nil
}

func (AwaitProfileCompletion) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("AwaitProfileCompletion"), nil
}

var (
	DiagnosticsCollectedChannel = dex.DefineChannel[string]("diagnosticsCollected")
	OnCallAcknowledgedChannel   = dex.DefineChannel[string]("onCallAcknowledged")
	IncidentResolvedChannel     = dex.DefineChannel[string]("incidentResolved")
)

// ExplicitTerminationFlow implements WCP-43 Explicit Termination: ForceComplete
// closes the Flow at once and cancels the Steps still waiting.
type ExplicitTerminationFlow struct{ dex.FlowDefaults }

func (*ExplicitTerminationFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartIncident{}),
		dex.DefineStep(CollectDiagnostics{}),
		dex.DefineStep(NotifyOnCall{}),
		dex.DefineStep(ResolveIncident{}),
	}
}

func (flow *ExplicitTerminationFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.ResolveIncident, nil)}
}

func (*ExplicitTerminationFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{DiagnosticsCollectedChannel, OnCallAcknowledgedChannel, IncidentResolvedChannel},
	}
}

// ResolveIncident resolves the incident, which ends the Flow.
func (*ExplicitTerminationFlow) ResolveIncident(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := IncidentResolvedChannel.Publish(ctx, "resolved"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type StartIncident struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (StartIncident) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(CollectDiagnostics{}, nil),
		dex.MovementOf(NotifyOnCall{}, nil),
		dex.MovementOf(ResolveIncident{}, nil),
	), nil
}

type CollectDiagnostics struct{ dex.StepDefaults }

func (CollectDiagnostics) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(DiagnosticsCollectedChannel.ForOne()), nil
}

func (CollectDiagnostics) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("CollectDiagnostics"), nil
}

type NotifyOnCall struct{ dex.StepDefaults }

func (NotifyOnCall) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(OnCallAcknowledgedChannel.ForOne()), nil
}

func (NotifyOnCall) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("NotifyOnCall"), nil
}

type ResolveIncident struct{ dex.StepDefaults }

func (ResolveIncident) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(IncidentResolvedChannel.ForOne()), nil
}

func (ResolveIncident) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.ForceComplete("ResolveIncident"), nil
}

var (
	_ dex.Flow = (*ImplicitTerminationFlow)(nil)
	_ dex.Flow = (*ExplicitTerminationFlow)(nil)
)
