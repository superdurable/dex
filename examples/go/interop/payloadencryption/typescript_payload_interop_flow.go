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

// TypeScriptPayloadInteropFlow is the Go Client's definition of the Flow the TypeScript Worker serves.
type TypeScriptPayloadInteropFlow struct {
	dex.FlowDefaults
}

func (*TypeScriptPayloadInteropFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(storeTypeScriptPayloadInteropSecretStep{})}
}

func (*TypeScriptPayloadInteropFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return payloadInteropPersistenceSchema()
}

type storeTypeScriptPayloadInteropSecretStep struct {
	dex.StepDefaultsNoWaitFor[PayloadInteropInput]
}

// GetStepType returns the Step type every SDK uses for this Flow's start Step.
func (storeTypeScriptPayloadInteropSecretStep) GetStepType() string {
	return "StorePayloadInteropSecretStep"
}

func (storeTypeScriptPayloadInteropSecretStep) GetStepOptions() *dex.StepOptions {
	return payloadInteropStepOptions()
}

// Execute mirrors the TypeScript Worker; the Go Client never runs it.
func (storeTypeScriptPayloadInteropSecretStep) Execute(
	ctx dex.Context, input PayloadInteropInput,
) (*dex.StepDecision, error) {
	if err := storePayloadInteropValues(ctx, OwnerTypeScript, input); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(ExpectedOutput(OwnerTypeScript, input.Text)), nil
}

var _ dex.Flow = (*TypeScriptPayloadInteropFlow)(nil)
