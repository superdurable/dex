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

// Package rpccancelingsteps shows an RPC that cancels a running Step: a user
// stops a report while it generates.
package rpccancelingsteps

import (
	"errors"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	ReportReady   = "ready"
	ReportStopped = "stopped"
	sectionCount  = 10
	sectionWork   = time.Second
)

var (
	ReportStatus   = dex.DefineAttribute[string]("ReportStatus")
	ReportProgress = dex.DefineStream[int]("ReportProgress", 1<<20)
)

type ReportGenerationFlow struct {
	dex.FlowDefaults
}

func NewReportGenerationFlow() *ReportGenerationFlow {
	return &ReportGenerationFlow{}
}

func (*ReportGenerationFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(GenerateReport{}),
		dex.DefineStep(DeliverReport{}),
		dex.DefineStep(FinishReportStop{}),
	}
}

func (flow *ReportGenerationFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.StopReport, &dex.RPCOptions{Timeout: 30 * time.Second}),
	}
}

func (*ReportGenerationFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{ReportStatus},
		Streams:    []dex.StreamDef{ReportProgress},
	}
}

func (*ReportGenerationFlow) StopReport(ctx dex.Context, _ dex.None) (*dex.RPCResult[bool], error) {
	status, err := getReportStatus(ctx)
	if err != nil {
		return nil, err
	}
	if status == ReportReady || status == ReportStopped {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	result := &dex.RPCResult[bool]{
		Output:    true,
		NextSteps: []dex.StepMovement{dex.MovementOf(FinishReportStop{}, nil)},
	}
	return result.CancelSteps(GenerateReport{}), nil
}

type GenerateReport struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (GenerateReport) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodTimeout: 5 * time.Minute,
		HeartbeatTimeout:     30 * time.Second,
	}
}

func (GenerateReport) Execute(ctx dex.Context, reportName string) (*dex.StepDecision, error) {
	for section := 1; section <= sectionCount; section++ {
		select {
		case <-ctx.Done():
			// A Stop cancels ctx; the result of this attempt is discarded.
			return nil, ctx.Err()
		case <-time.After(sectionWork):
		}
		if err := ReportProgress.Write(ctx, section); err != nil {
			return nil, err
		}
	}
	if err := ReportStatus.Set(ctx, ReportReady); err != nil {
		return nil, err
	}
	return dex.GoTo(DeliverReport{}, reportName), nil
}

type DeliverReport struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (DeliverReport) Execute(_ dex.Context, reportName string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(reportName + " delivered"), nil
}

type FinishReportStop struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (FinishReportStop) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	status, err := getReportStatus(ctx)
	if err != nil {
		return nil, err
	}
	// The report can commit after StopReport read its snapshot.
	if status == ReportReady {
		return dex.DeadEnd(), nil
	}
	if err := ReportStatus.Set(ctx, ReportStopped); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(ReportStopped), nil
}

// getReportStatus returns the committed status, or empty while the report generates.
func getReportStatus(ctx dex.Context) (string, error) {
	status, err := ReportStatus.Get(ctx)
	var notFound *dex.AttributeNotFoundError
	if errors.As(err, &notFound) {
		return "", nil
	}
	return status, err
}

var _ dex.Flow = (*ReportGenerationFlow)(nil)
