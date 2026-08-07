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
	"github.com/superdurable/dex/sdk-go/dex"
)

const EmployerIDSearchKey = "CustomKeywordField"

var (
	EmployerOptInEmployerID = dex.DefineAttribute[string](
		"EMPLOYER_OPT_IN_EmployerId",
		dex.Indexed(dex.AttributeIndex{
			Type:     dex.IndexKeyword,
			IndexKey: EmployerIDSearchKey,
		}),
	)
	EmployerOptInStatus = dex.DefineAttribute[bool]("EMPLOYER_OPT_IN_Status")
)

type EmployerOptInInput struct {
	EmployerID string
}

type EmployerOptInFlow struct {
	dex.FlowDefaults
}

func NewEmployerOptInFlow() *EmployerOptInFlow {
	return &EmployerOptInFlow{}
}

func (*EmployerOptInFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(optInStep{}),
		dex.DefineStep(optOutStep{}),
	}
}

func (*EmployerOptInFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{EmployerOptInEmployerID, EmployerOptInStatus},
	}
}

func (*EmployerOptInFlow) IsOptedIn(
	ctx dex.Context,
	_ dex.None,
) (dex.RPCResult[bool], error) {
	optedIn, err := EmployerOptInStatus.Get(ctx)
	if err != nil {
		return dex.RPCResult[bool]{}, err
	}
	return dex.RPCResult[bool]{Output: optedIn}, nil
}

func (*EmployerOptInFlow) OptOut(
	_ dex.Context,
	_ dex.None,
) (dex.RPCResult[dex.None], error) {
	return dex.RPCResult[dex.None]{
		NextSteps: []dex.StepMovement{dex.MovementOf(optOutStep{}, nil)},
	}, nil
}

type optInStep struct {
	dex.StepDefaultsNoWaitFor[EmployerOptInInput]
}

func (optInStep) Execute(
	ctx dex.Context,
	input EmployerOptInInput,
) (*dex.StepDecision, error) {
	if err := EmployerOptInEmployerID.Set(ctx, input.EmployerID); err != nil {
		return nil, err
	}
	if err := EmployerOptInStatus.Set(ctx, true); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type optOutStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (optOutStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	if err := EmployerOptInStatus.Set(ctx, false); err != nil {
		return nil, err
	}
	return dex.ForceComplete(nil), nil
}

var _ dex.Flow = (*EmployerOptInFlow)(nil)
