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

package controllongrunningwork

import (
	"errors"
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	TotalRecords     = 1000
	DefaultBatchSize = 10
	MaxBatchSize     = 1000
	BatchInterval    = time.Second
)

var (
	BatchSize     = dex.DefineAttribute[int]("batchSize")
	StopRequested = dex.DefineAttribute[bool]("stopRequested")
)

// BackfillProgress is the range of records that the loop has not processed yet.
type BackfillProgress struct {
	NextRecord int `json:"nextRecord"`
	LastRecord int `json:"lastRecord"`
}

// RecordBackfillFlow processes records in batches that two control Attributes steer.
type RecordBackfillFlow struct{ dex.FlowDefaults }

func NewRecordBackfillFlow() *RecordBackfillFlow {
	return &RecordBackfillFlow{}
}

func (*RecordBackfillFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartRecordBackfill{}),
		dex.DefineStep(ProcessRecordBatch{}),
	}
}

func (flow *RecordBackfillFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.SetBatchSize, nil),
		dex.DefineRPC(flow.RequestStop, nil),
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (*RecordBackfillFlow) GetDexSummary(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*RecordBackfillFlow) GetDexDisplay(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*RecordBackfillFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{BatchSize, StopRequested},
	}
}

// SetBatchSize changes how many records each later batch processes.
func (*RecordBackfillFlow) SetBatchSize(ctx dex.Context, size int) (*dex.RPCResult[dex.None], error) {
	if size < 1 || size > MaxBatchSize {
		return nil, fmt.Errorf("batch size must be between 1 and %d", MaxBatchSize)
	}
	if err := BatchSize.Set(ctx, size); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// RequestStop asks the loop to stop before its next batch.
func (*RecordBackfillFlow) RequestStop(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := StopRequested.Set(ctx, true); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:group group-id:start group-label:"Start"
// dex:explanation text:"Start the backfill at the first record."
type StartRecordBackfill struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (StartRecordBackfill) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	progress := BackfillProgress{NextRecord: 1, LastRecord: TotalRecords}
	return dex.GoTo(ProcessRecordBatch{}, progress), nil
}

// dex:group group-id:batches group-label:"Batches"
// dex:explanation text:"Read the controls, then process one batch of records or stop."
type ProcessRecordBatch struct {
	dex.StepDefaults
}

func (ProcessRecordBatch) WaitFor(dex.Context, BackfillProgress) (*dex.Wait, error) {
	return dex.Until(dex.Timer(BatchInterval)), nil
}

func (ProcessRecordBatch) Execute(ctx dex.Context, progress BackfillProgress) (*dex.StepDecision, error) {
	stop, err := StopRequested.Get(ctx)
	if err != nil && !isAttributeNotFound(err) {
		return nil, err
	}
	if stop {
		return dex.GracefulComplete(fmt.Sprintf("stopped before record %d", progress.NextRecord)), nil
	}
	size, err := BatchSize.Get(ctx)
	if isAttributeNotFound(err) {
		size, err = DefaultBatchSize, nil
	}
	if err != nil {
		return nil, err
	}
	last := min(progress.NextRecord+size-1, progress.LastRecord)
	fmt.Printf("processing records %d to %d\n", progress.NextRecord, last)
	if last == progress.LastRecord {
		return dex.GracefulComplete(fmt.Sprintf("processed %d records", last)), nil
	}
	next := BackfillProgress{NextRecord: last + 1, LastRecord: progress.LastRecord}
	return dex.GoTo(ProcessRecordBatch{}, next), nil
}

func isAttributeNotFound(err error) bool {
	var notFound *dex.AttributeNotFoundError
	return errors.As(err, &notFound)
}

var (
	_ dex.Flow                    = (*RecordBackfillFlow)(nil)
	_ dex.RPC[int, dex.None]      = (*RecordBackfillFlow)(nil).SetBatchSize
	_ dex.RPC[dex.None, dex.None] = (*RecordBackfillFlow)(nil).RequestStop
)
