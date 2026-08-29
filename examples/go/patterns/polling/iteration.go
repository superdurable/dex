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

package polling

import (
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

type IterationFlow struct{ dex.FlowDefaults }

func NewIterationFlow() *IterationFlow { return &IterationFlow{} }

func (*IterationFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(iterationStep{})}
}

func (*IterationFlow) GetPersistenceSchema() dex.PersistenceSchema { return dex.PersistenceSchema{} }

type iterationStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (iterationStep) GetStepType() string { return "IterationStep" }

func (iterationStep) Execute(ctx dex.Context, pageToken string) (*dex.StepDecision, error) {
	documents, nextPageToken := readPage(pageToken)
	fmt.Printf("Migrating %d documents from page %q\n", len(documents), pageToken)
	if nextPageToken == "" {
		return dex.GracefulComplete(nil), nil
	}
	return dex.GoTo(iterationStep{}, nextPageToken), nil
}

func readPage(pageToken string) ([]string, string) {
	switch pageToken {
	case "":
		return []string{"document-1", "document-2"}, "page-2"
	case "page-2":
		return []string{"document-3"}, "page-3"
	default:
		return []string{"document-4"}, ""
	}
}

var _ dex.Flow = (*IterationFlow)(nil)
