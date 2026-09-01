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

import (
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

type SubFlowParentFlow struct {
	dex.FlowDefaults
	child *SubFlowChildFlow
}

func NewSubFlowParentFlow(child *SubFlowChildFlow) *SubFlowParentFlow {
	return &SubFlowParentFlow{child: child}
}

func (flow *SubFlowParentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(subFlowParentStep{child: flow.child})}
}

func (*SubFlowParentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type subFlowParentStep struct {
	dex.StepDefaults
	child *SubFlowChildFlow
}

func (step subFlowParentStep) WaitFor(_ dex.Context, input int) (*dex.Wait, error) {
	timeout := time.Hour
	return dex.Until(dex.SubFlow(step.child, input, dex.SubFlowOptions{
		Timeout:       &timeout,
		TimeoutPolicy: dex.TimeoutCancel,
	})), nil
}

func (subFlowParentStep) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	result, err := dex.SubFlowResult(ctx)
	if err != nil {
		return nil, err
	}
	var output int
	if err := result.DecodeSingleOutput(&output); err != nil {
		return nil, err
	}
	flowID, err := dex.SubFlowID(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(fmt.Sprintf("%s|%d", flowID, output)), nil
}

var _ dex.Flow = (*SubFlowParentFlow)(nil)
