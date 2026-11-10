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

package criticalsection

import (
	"errors"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const contractEditDuration = 100 * time.Millisecond

// ContractEdit is one reviewer's turn to edit the shared contract.
// The Should* fields simulate a failure or a withdrawal for the example.
type ContractEdit struct {
	Reviewer          string `json:"reviewer"`
	ShouldFailHandoff bool   `json:"shouldFailHandoff"`
	ShouldWithdraw    bool   `json:"shouldWithdraw"`
	ShouldFailEdit    bool   `json:"shouldFailEdit"`
	StartedAtMillis   int64  `json:"startedAtMillis"`
}

// ContractEditRecord reports when one edit held the contract and how it ended.
type ContractEditRecord struct {
	Reviewer         string `json:"reviewer"`
	Outcome          string `json:"outcome"`
	StartedAtMillis  int64  `json:"startedAtMillis"`
	FinishedAtMillis int64  `json:"finishedAtMillis"`
}

var (
	ContractEditToken    = dex.DefineChannel[string]("contractEditToken")
	ContractEditFinished = dex.DefineChannel[ContractEditRecord]("contractEditFinished")
	errContractHandoff   = errors.New("contract handoff failed")
	errContractEdit      = errors.New("contract edit failed")
	contractEditRetry    = &dex.RetryPolicy{InitialInterval: time.Second, MaximumAttempts: 2}
)

// ContractEditFlow lets several reviewers edit one contract, one at a time.
// A one-message Channel is the token that guards the edit section.
type ContractEditFlow struct{ dex.FlowDefaults }

func NewContractEditFlow() *ContractEditFlow {
	return &ContractEditFlow{}
}

func (*ContractEditFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartContractEdits{}),
		dex.DefineStep(EnterContractEdit{}),
		dex.DefineStep(EditContract{}),
		dex.DefineStep(SaveContractEdit{}),
		dex.DefineStep(ReleaseContractEditToken{}),
		dex.DefineStep(FinishContractEdits{}),
	}
}

func (*ContractEditFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{ContractEditToken, ContractEditFinished},
	}
}

// StartContractEdits publishes the only token and starts one branch per reviewer.
type StartContractEdits struct {
	dex.StepDefaultsNoWaitFor[[]ContractEdit]
}

func (StartContractEdits) Execute(ctx dex.Context, edits []ContractEdit) (*dex.StepDecision, error) {
	if err := ContractEditToken.Publish(ctx, "contract"); err != nil {
		return nil, err
	}
	movements := make([]dex.StepMovement, 0, len(edits)+1)
	for _, edit := range edits {
		movements = append(movements, dex.MovementOf(EnterContractEdit{}, edit))
	}
	movements = append(movements, dex.MovementOf(FinishContractEdits{}, len(edits)))
	return dex.GoToMany(movements...), nil
}

// EnterContractEdit takes the token and hands off; it does no protected work,
// so canceling it is safe.
type EnterContractEdit struct{ dex.StepDefaults }

func (EnterContractEdit) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry:   contractEditRetry,
		ExecuteFailure: dex.ProceedToOnExecuteFailure(ReleaseContractEditToken{}, nil),
	}
}

func (EnterContractEdit) WaitFor(dex.Context, ContractEdit) (*dex.Wait, error) {
	return dex.Until(ContractEditToken.ForOne()), nil
}

func (EnterContractEdit) Execute(_ dex.Context, edit ContractEdit) (*dex.StepDecision, error) {
	if edit.ShouldFailHandoff {
		return nil, errContractHandoff
	}
	return dex.GoTo(EditContract{}, edit), nil
}

// EditContract is the first Step that works on the contract. Never cancel it.
type EditContract struct {
	dex.StepDefaultsNoWaitFor[ContractEdit]
}

func (EditContract) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry:   contractEditRetry,
		ExecuteFailure: dex.ProceedToOnExecuteFailure(ReleaseContractEditToken{}, nil),
	}
}

func (EditContract) Execute(ctx dex.Context, edit ContractEdit) (*dex.StepDecision, error) {
	edit.StartedAtMillis = time.Now().UnixMilli()
	if edit.ShouldWithdraw {
		if err := leaveContractEdit(ctx, edit, "withdrawn"); err != nil {
			return nil, err
		}
		return dex.DeadEnd(), nil
	}
	time.Sleep(contractEditDuration)
	return dex.GoTo(SaveContractEdit{}, edit), nil
}

// SaveContractEdit is the last Step of the section; it publishes the token back.
type SaveContractEdit struct {
	dex.StepDefaultsNoWaitFor[ContractEdit]
}

func (SaveContractEdit) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry:   contractEditRetry,
		ExecuteFailure: dex.ProceedToOnExecuteFailure(ReleaseContractEditToken{}, nil),
	}
}

func (SaveContractEdit) Execute(ctx dex.Context, edit ContractEdit) (*dex.StepDecision, error) {
	time.Sleep(contractEditDuration)
	if edit.ShouldFailEdit {
		return nil, errContractEdit
	}
	if err := leaveContractEdit(ctx, edit, "saved"); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

// ReleaseContractEditToken is the failure exit of the section.
type ReleaseContractEditToken struct {
	dex.StepDefaultsNoWaitFor[ContractEdit]
}

func (ReleaseContractEditToken) Execute(ctx dex.Context, edit ContractEdit) (*dex.StepDecision, error) {
	if err := leaveContractEdit(ctx, edit, "failed"); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

// FinishContractEdits joins the branches after every reviewer leaves the section.
type FinishContractEdits struct{ dex.StepDefaults }

func (FinishContractEdits) WaitFor(_ dex.Context, editCount int) (*dex.Wait, error) {
	return dex.Until(ContractEditFinished.ForN(editCount)), nil
}

func (FinishContractEdits) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	records, err := ContractEditFinished.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(records), nil
}

// leaveContractEdit records the edit and publishes the token for the next reviewer.
// An edit that never started has an empty interval at its finish time.
func leaveContractEdit(ctx dex.Context, edit ContractEdit, outcome string) error {
	finishedAtMillis := time.Now().UnixMilli()
	startedAtMillis := edit.StartedAtMillis
	if startedAtMillis == 0 {
		startedAtMillis = finishedAtMillis
	}
	record := ContractEditRecord{
		Reviewer:         edit.Reviewer,
		Outcome:          outcome,
		StartedAtMillis:  startedAtMillis,
		FinishedAtMillis: finishedAtMillis,
	}
	if err := ContractEditFinished.Publish(ctx, record); err != nil {
		return err
	}
	return ContractEditToken.Publish(ctx, "contract")
}

var _ dex.Flow = (*ContractEditFlow)(nil)
