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

package payloadencryptioninterop

import "github.com/superdurable/dex/sdk-go/dex"

// GoPayloadInteropFlow is the interop Flow that the Go Worker serves.
type GoPayloadInteropFlow struct {
	dex.FlowDefaults
}

func (*GoPayloadInteropFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(storeGoPayloadInteropSecretStep{})}
}

func (*GoPayloadInteropFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return payloadInteropPersistenceSchema()
}

type storeGoPayloadInteropSecretStep struct {
	dex.StepDefaultsNoWaitFor[PayloadInteropInput]
}

// GetStepType returns the Step type every SDK uses for this Flow's start Step.
func (storeGoPayloadInteropSecretStep) GetStepType() string {
	return "StorePayloadInteropSecretStep"
}

func (storeGoPayloadInteropSecretStep) GetStepOptions() *dex.StepOptions {
	return payloadInteropStepOptions()
}

func (storeGoPayloadInteropSecretStep) Execute(ctx dex.Context, input PayloadInteropInput) (*dex.StepDecision, error) {
	if err := storePayloadInteropValues(ctx, OwnerGo, input); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(ExpectedOutput(OwnerGo, input.Text)), nil
}

var _ dex.Flow = (*GoPayloadInteropFlow)(nil)
