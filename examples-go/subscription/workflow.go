// Package subscription demonstrates a multi-step email subscription workflow.
//
// Flow: Signup -> WaitForVerification (channel + timer) -> Activate -> Complete
package subscription

import (
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	keyEmail    = dex.NewStateKey[string]("Email")
	keyPlan     = dex.NewStateKey[string]("Plan")
	keyVerified = dex.NewStateKey[bool]("Verified")
	keyActive   = dex.NewStateKey[bool]("Active")
)

// --- Channels ---

type VerifyEvent struct {
	Code string
}

var VerifyChannel = dex.NewChannel[VerifyEvent]("verify")

type NotificationEvent struct {
	Email   string
	Message string
}

var NotifyChannel = dex.NewChannel[NotificationEvent]("notify")

// --- Flow ---

type SubscriptionFlow struct {
	dex.DefaultFlowType
}

func (f *SubscriptionFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(keyEmail),
			dex.DefineStateKey(keyPlan),
			dex.DefineStateKey(keyVerified),
			dex.DefineStateKey(keyActive),
		},
		Channels: []dex.ChannelDef{
			dex.DefineChannel(VerifyChannel),
			dex.DefineChannel(NotifyChannel),
		},
	}
}

func (f *SubscriptionFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep(&SignupStep{}),
		dex.NonStartingStep(&WaitVerifyStep{}),
		dex.NonStartingStep(&ActivateStep{}),
	}
}

// --- Step 1: Signup (no wait) ---

type SignupInput struct {
	Email string
	Plan  string
}

type SignupStep struct {
	dex.StepDefaults[SignupInput]
}

func (s *SignupStep) Execute(ctx dex.Context, input SignupInput) (dex.StepDecision, error) {
	if err := keyEmail.SetValue(ctx, input.Email); err != nil {
		return nil, err
	}
	if err := keyPlan.SetValue(ctx, input.Plan); err != nil {
		return nil, err
	}
	return dex.GoTo(&WaitVerifyStep{}, input.Email), nil
}

// --- Step 2: Wait for email verification ---

type WaitVerifyStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *WaitVerifyStep) WaitFor(_ dex.Context, _ string) (dex.WaitForCondition, error) {
	return dex.AnyOf(
		VerifyChannel.Condition(),
		dex.Timer(24*time.Hour),
	), nil
}

func (s *WaitVerifyStep) Execute(ctx dex.Context, _ string) (dex.StepDecision, error) {
	msgs, err := VerifyChannel.GetConsumedMessages(ctx)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return dex.Fail("verification timeout"), nil
	}

	if err := keyVerified.SetValue(ctx, true); err != nil {
		return nil, err
	}
	return dex.GoTo(&ActivateStep{}, ""), nil
}

// --- Step 3: Activate subscription ---

type ActivateStep struct {
	dex.StepDefaults[string]
}

func (s *ActivateStep) Execute(ctx dex.Context, _ string) (dex.StepDecision, error) {
	email, err := keyEmail.GetValue(ctx)
	if err != nil {
		return nil, err
	}
	plan, err := keyPlan.GetValue(ctx)
	if err != nil {
		return nil, err
	}
	verified, err := keyVerified.GetValue(ctx)
	if err != nil {
		return nil, err
	}

	if err := keyActive.SetValue(ctx, true); err != nil {
		return nil, err
	}
	if err := NotifyChannel.Publish(ctx, NotificationEvent{
		Email:   email,
		Message: "subscription activated",
	}); err != nil {
		return nil, err
	}

	return dex.Complete(map[string]any{
		"email":    email,
		"plan":     plan,
		"verified": verified,
		"active":   true,
	}), nil
}
