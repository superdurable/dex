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

package scalableparallel

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const ParentWorkflowIDAttributeName = "ParentWorkflowId"

var ParentWorkflowID = dex.DefineAttribute[string](ParentWorkflowIDAttributeName)

type ChildFlow struct {
	dex.FlowDefaults
	getClient func() *dex.Client
	getParent func() *ParentFlow
}

func NewChildFlow(
	getClient func() *dex.Client,
	getParent func() *ParentFlow,
) *ChildFlow {
	return &ChildFlow{getClient: getClient, getParent: getParent}
}

func (flow *ChildFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(processingStep{
			getClient: flow.getClient,
			getParent: flow.getParent,
		}),
	}
}

func (*ChildFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{ParentWorkflowID},
	}
}

type processingStep struct {
	dex.StepDefaults
	getClient func() *dex.Client
	getParent func() *ParentFlow
}

func (processingStep) WaitFor(
	ctx dex.Context,
	input string,
) (*dex.Wait, error) {
	delay := time.Duration(rand.Intn(60)) * time.Second
	return dex.Until(dex.Timer(delay)), nil
}

func (step processingStep) Execute(
	ctx dex.Context,
	input string,
) (*dex.StepDecision, error) {
	parentID, err := ParentWorkflowID.Get(ctx)
	if err == nil && parentID != "" {
		client := step.getClient()
		parent := step.getParent()
		if client != nil && parent != nil {
			var output dex.None
			rpcErr := client.InvokeRPC(
				context.Background(),
				parentID,
				parent.CompleteChildWorkflow,
				ctx.FlowID(),
				&output,
				dex.InvokeOptions{},
			)
			if rpcErr != nil {
				var inactive *dex.FlowNotActiveError
				if errors.As(rpcErr, &inactive) {
					fmt.Println(
						"Parent workflow may have completed, might be duplicate " +
							"completion request, ignore it.",
					)
				} else {
					return nil, rpcErr
				}
			}
		}
	}
	fmt.Printf("ChildFlow completed processing: %s (%s)\n", input, ctx.FlowID())
	return dex.GracefulComplete(nil), nil
}

var _ dex.Flow = (*ChildFlow)(nil)
