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

package shortlistcandidates

import (
	"context"
	"fmt"
	"time"

	"github.com/superdurable/dex/examples/go/workflows/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	ShortlistEmployerID = dex.DefineAttribute[string](
		"SHORTLIST_EmployerId",
		dex.Indexed(dex.AttributeIndex{
			Type:     dex.IndexKeyword,
			IndexKey: EmployerIDSearchKey,
		}),
	)
	ShortlistCandidateID        = dex.DefineAttribute[string]("SHORTLIST_CandidateId")
	ShortlistEmailSentTimestamp = dex.DefineAttribute[int64]("SHORTLIST_EmailSentTimestamp")
	RevokeShortlist             = dex.DefineChannel[dex.None]("SHORTLIST_SIGNAL_RevokeShortlist")
)

type ShortlistInput struct {
	EmployerID  string
	CandidateID string
}

type ShortlistFlow struct {
	dex.FlowDefaults
	service       service.MyService
	getClient     func() *dex.Client
	employerOptIn *EmployerOptInFlow
}

func NewShortlistFlow(
	applicationService service.MyService,
	getClient func() *dex.Client,
	employerOptIn *EmployerOptInFlow,
) *ShortlistFlow {
	return &ShortlistFlow{
		service:       applicationService,
		getClient:     getClient,
		employerOptIn: employerOptIn,
	}
}

func (flow *ShortlistFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(shortlistStep{}),
		dex.DefineStep(sendEmailStep{
			service:       flow.service,
			getClient:     flow.getClient,
			employerOptIn: flow.employerOptIn,
		}),
	}
}

func (*ShortlistFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{
			ShortlistEmployerID,
			ShortlistCandidateID,
			ShortlistEmailSentTimestamp,
		},
		Channels: []dex.ChannelDef{RevokeShortlist},
	}
}

func (*ShortlistFlow) GetEmailSentTimestamp(
	ctx dex.Context,
	_ dex.None,
) (dex.RPCResult[int64], error) {
	timestamp, err := ShortlistEmailSentTimestamp.Get(ctx)
	return dex.RPCResult[int64]{Output: timestamp}, err
}

type shortlistStep struct {
	dex.StepDefaultsNoWaitFor[ShortlistInput]
}

func (shortlistStep) Execute(
	ctx dex.Context,
	input ShortlistInput,
) (*dex.StepDecision, error) {
	if err := ShortlistEmployerID.Set(ctx, input.EmployerID); err != nil {
		return nil, err
	}
	if err := ShortlistCandidateID.Set(ctx, input.CandidateID); err != nil {
		return nil, err
	}
	if err := ShortlistEmailSentTimestamp.Set(ctx, 0); err != nil {
		return nil, err
	}
	return dex.GoTo(sendEmailStep{}, nil), nil
}

type sendEmailStep struct {
	dex.StepDefaults
	service       service.MyService
	getClient     func() *dex.Client
	employerOptIn *EmployerOptInFlow
}

func (sendEmailStep) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.AnyOf(dex.Timer(5*time.Minute), RevokeShortlist.ForOne()), nil
}

func (step sendEmailStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	employer, err := ShortlistEmployerID.Get(ctx)
	if err != nil {
		return nil, err
	}
	candidate, err := ShortlistCandidateID.Get(ctx)
	if err != nil {
		return nil, err
	}
	results, err := RevokeShortlist.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(results) > 0 {
		fmt.Printf(
			"Not sending the email to %s-%s because of revoking\n",
			employer,
			candidate,
		)
		return dex.ForceComplete(nil), nil
	}
	optedIn, err := IsOptedIn(
		context.Background(),
		step.getClient(),
		step.employerOptIn,
		employer,
	)
	if err != nil {
		return nil, err
	}
	if !optedIn {
		fmt.Printf(
			"Not sending the email to %s-%s because of not opted-in\n",
			employer,
			candidate,
		)
		return dex.ForceComplete(nil), nil
	}
	step.service.SendEmail(
		employer+"-"+candidate,
		fmt.Sprintf("Employer %s wants to know more about you", employer),
		"Hello xxx, ...",
	)
	if err := ShortlistEmailSentTimestamp.Set(ctx, time.Now().UnixMilli()); err != nil {
		return nil, err
	}
	return dex.ForceComplete(nil), nil
}

var _ dex.Flow = (*ShortlistFlow)(nil)
