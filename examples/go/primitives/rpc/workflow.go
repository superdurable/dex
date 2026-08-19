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

package rpc

import "github.com/superdurable/dex/sdk-go/dex"

var (
	Internal = dex.DefineChannel[dex.None]("rpc-internal")
	Data     = dex.DefineAttribute[string]("rpc-data")
)

type RpcFlow struct {
	dex.FlowDefaults
}

func NewRpcFlow() *RpcFlow {
	return &RpcFlow{}
}

func (*RpcFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(rpcWaitStep{}),
		dex.DefineStep(rpcCompleteStep{}),
	}
}

func (*RpcFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{Data},
		Channels:   []dex.ChannelDef{Internal},
	}
}

type rpcWaitStep struct {
	dex.StepDefaults
}

func (rpcWaitStep) WaitFor(_ dex.Context, _ int) (*dex.Wait, error) {
	return dex.Until(Internal.ForOne()), nil
}

func (rpcWaitStep) Execute(_ dex.Context, _ int) (*dex.StepDecision, error) {
	return dex.GoTo(rpcCompleteStep{}, 0), nil
}

type rpcCompleteStep struct {
	dex.StepDefaultsNoWaitFor[int]
}

func (rpcCompleteStep) Execute(_ dex.Context, input int) (*dex.StepDecision, error) {
	return dex.GracefulComplete(input + 1), nil
}

func (*RpcFlow) Trigger(ctx dex.Context, input string) (*dex.RPCResult[string], error) {
	if err := Data.Set(ctx, input); err != nil {
		return nil, err
	}
	if err := Internal.Publish(ctx, nil); err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: input}, nil
}

var _ dex.Flow = (*RpcFlow)(nil)
