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

package subflow

import "github.com/superdurable/dex/sdk-go/dex"

var SelectionMessages = dex.DefineChannel[string]("selection-messages")

type SubFlowSelectionFlow struct {
	dex.FlowDefaults
}

func (*SubFlowSelectionFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(awaitChildAlone{}),
		dex.DefineStep(awaitChildOrMessage{}),
		dex.DefineStep(awaitChildrenTogether{}),
	}
}

func (flow *SubFlowSelectionFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (*SubFlowSelectionFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{SelectionMessages}}
}

func (*SubFlowSelectionFlow) GetDexSummary(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*SubFlowSelectionFlow) GetDexDisplay(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

// dex:group group-id:children group-label:"Child completion"
// dex:explanation text:"Waits for one child Flow to complete."
type awaitChildAlone struct{ dex.StepDefaults }

func (awaitChildAlone) WaitFor(_ dex.Context, input int) (*dex.Wait, error) {
	return dex.Until(dex.SubFlow(&SubFlowChildFlow{}, input)), nil
}

func (awaitChildAlone) Execute(_ dex.Context, input int) (*dex.StepDecision, error) {
	return dex.GoTo(awaitChildOrMessage{}, input), nil
}

// dex:group group-id:children group-label:"Child completion"
// dex:explanation text:"Waits for a child Flow or a Channel message."
type awaitChildOrMessage struct{ dex.StepDefaults }

func (awaitChildOrMessage) WaitFor(_ dex.Context, input int) (*dex.Wait, error) {
	return dex.AnyOf(childCompletionCondition(&SubFlowChildFlow{}, input), SelectionMessages.ForOne()), nil
}

func (awaitChildOrMessage) Execute(_ dex.Context, input int) (*dex.StepDecision, error) {
	return dex.GoTo(awaitChildrenTogether{}, input), nil
}

// dex:group group-id:children group-label:"Child completion"
// dex:explanation text:"Waits for both child Flows to complete."
type awaitChildrenTogether struct{ dex.StepDefaults }

func (awaitChildrenTogether) WaitFor(_ dex.Context, input int) (*dex.Wait, error) {
	return dex.AllOf(childCompletionCondition(&SubFlowChildFlow{}, input), childCompletionCondition(&SubFlowChildFlow{}, input+1)), nil
}

func (awaitChildrenTogether) Execute(_ dex.Context, input int) (*dex.StepDecision, error) {
	return dex.GracefulComplete(input), nil
}

func childCompletionCondition(child *SubFlowChildFlow, input int) dex.Condition {
	return dex.SubFlow(child, input)
}

var _ dex.Flow = (*SubFlowSelectionFlow)(nil)
