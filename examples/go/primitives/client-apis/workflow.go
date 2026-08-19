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

package clientapis

import "github.com/superdurable/dex/sdk-go/dex"

const keywordKey = "CustomKeywordField"

var Keyword = dex.DefineAttribute[string](
	keywordKey,
	dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}),
)

type ClientApisFlow struct {
	dex.FlowDefaults
}

func NewClientApisFlow() *ClientApisFlow {
	return &ClientApisFlow{}
}

func (*ClientApisFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(clientApisStep{})}
}

func (*ClientApisFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{Keyword}}
}

type clientApisStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (clientApisStep) Execute(ctx dex.Context, input string) (*dex.StepDecision, error) {
	if err := Keyword.Set(ctx, input); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(input), nil
}

var _ dex.Flow = (*ClientApisFlow)(nil)
