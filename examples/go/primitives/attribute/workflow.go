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

package attribute

import (
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

var (
	Status = dex.DefineAttribute[string](
		"primitive-attribute-status",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword, IndexKey: "OrderStatus"}),
	)
	Email = dex.DefineAttribute[string](
		"primitive-attribute-email",
		dex.SyncToAttributeStore(),
	)
	Progress = dex.DefineAttributeMap[string](
		"primitive-attribute-progress",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword, IndexKey: "OrderProgress"}),
	)
	AttributeStoreConfig = &dex.FlowConfig{AttributeStoreName: ptr.Any("profiles")}
)

type AttributeFlow struct {
	dex.FlowDefaults
}

func NewAttributeFlow() *AttributeFlow {
	return &AttributeFlow{}
}

func (*AttributeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(attributeStep{})}
}

func (*AttributeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{Status, Progress, Email}}
}

type attributeStep struct {
	dex.StepDefaults
}

func (attributeStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(Status)},
	}
}

func (attributeStep) WaitFor(ctx dex.Context, input string) (*dex.Wait, error) {
	if err := Status.Set(ctx, "processing"); err != nil {
		return nil, err
	}
	if err := Progress.Set(ctx, "payment", "authorized"); err != nil {
		return nil, err
	}
	return dex.SkipWaitImmediately(), nil
}

func (attributeStep) Execute(ctx dex.Context, input string) (*dex.StepDecision, error) {
	if err := Status.Set(ctx, "completed"); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(input), nil
}

var _ dex.Flow = (*AttributeFlow)(nil)
