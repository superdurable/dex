// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

// gRPC's own default receive limit, which these tests exceed.
const grpcDefaultReceiveLimitBytes = 4 << 20

// Three texts of this size exceed the server's default 32 MiB send limit together.
const aboveServerLimitTextBytes = 12 << 20

var (
	messageSizeTextNames = []string{"a", "b", "c"}
	messageSizeTexts     = map[string]dex.Attribute[string]{
		"a": dex.DefineAttribute[string]("message-size-text-a"),
		"b": dex.DefineAttribute[string]("message-size-text-b"),
		"c": dex.DefineAttribute[string]("message-size-text-c"),
	}
	messageSizeGenerated = dex.DefineChannel[string]("message-size-generated")
)

type messageSizeFlow struct {
	dex.FlowDefaults
}

type messageSizeTextInput struct {
	Name  string
	Bytes int
}

func (messageSizeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(messageSizeReadStep{}),
		dex.DefineStep(messageSizeOutputStep{}),
	}
}

func (flow messageSizeFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.EchoText, nil),
		dex.DefineRPC(flow.GenerateText, nil),
		dex.DefineRPC(flow.WriteText, nil),
	}
}

func (messageSizeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	attributes := make([]dex.AttributeDef, 0, len(messageSizeTextNames))
	for _, name := range messageSizeTextNames {
		attributes = append(attributes, messageSizeTexts[name])
	}
	return dex.PersistenceSchema{
		Attributes: attributes,
		Channels:   []dex.ChannelDef{messageSizeGenerated},
	}
}

func (messageSizeFlow) EchoText(_ dex.Context, text string) (*dex.RPCResult[string], error) {
	return &dex.RPCResult[string]{Output: text}, nil
}

func (messageSizeFlow) GenerateText(
	ctx dex.Context,
	input messageSizeTextInput,
) (*dex.RPCResult[dex.None], error) {
	if err := messageSizeTexts[input.Name].Set(ctx, strings.Repeat(input.Name, input.Bytes)); err != nil {
		return nil, err
	}
	if err := messageSizeGenerated.Publish(ctx, input.Name); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// WriteText sets a text without publishing, so the read Step stays waiting.
func (messageSizeFlow) WriteText(
	ctx dex.Context,
	input messageSizeTextInput,
) (*dex.RPCResult[dex.None], error) {
	if err := messageSizeTexts[input.Name].Set(ctx, strings.Repeat(input.Name, input.Bytes)); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type messageSizeReadStep struct {
	dex.StepDefaults
}

func (messageSizeReadStep) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(messageSizeGenerated.ForN(len(messageSizeTextNames))), nil
}

func (messageSizeReadStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	movements := make([]dex.StepMovement, 0, len(messageSizeTextNames))
	for _, name := range messageSizeTextNames {
		if _, err := messageSizeTexts[name].Get(ctx); err != nil {
			return nil, fmt.Errorf("read text %q: %w", name, err)
		}
		movements = append(movements, dex.MovementOf(messageSizeOutputStep{}, name))
	}
	return dex.GoToMany(movements...), nil
}

type messageSizeOutputStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (messageSizeOutputStep) Execute(ctx dex.Context, name string) (*dex.StepDecision, error) {
	text, err := messageSizeTexts[name].Get(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(text), nil
}

// messageSizeUncachedFlow runs only on its own Worker, whose cache keeps no value.
type messageSizeUncachedFlow struct {
	dex.FlowDefaults
}

func (messageSizeUncachedFlow) GetSteps() []dex.StepDef {
	return nil
}

func (flow messageSizeUncachedFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.WriteText, nil),
		dex.DefineRPC(flow.SumTextLengths, nil),
	}
}

func (messageSizeUncachedFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return messageSizeFlow{}.GetPersistenceSchema()
}

func (messageSizeUncachedFlow) WriteText(
	ctx dex.Context,
	input messageSizeTextInput,
) (*dex.RPCResult[dex.None], error) {
	if err := messageSizeTexts[input.Name].Set(ctx, strings.Repeat(input.Name, input.Bytes)); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (messageSizeUncachedFlow) SumTextLengths(ctx dex.Context, _ dex.None) (*dex.RPCResult[int], error) {
	total := 0
	for _, name := range messageSizeTextNames {
		text, err := messageSizeTexts[name].Get(ctx)
		if err != nil {
			return nil, fmt.Errorf("read text %q: %w", name, err)
		}
		total += len(text)
	}
	return &dex.RPCResult[int]{Output: total}, nil
}

func TestMessageSizeDefaultLimitCarriesValuesAboveGrpcDefault(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "message-size-default")
	_, err := integClient.StartFlow(ctx, messageSizeFlow{}, flowID, nil, dex.StartFlowOptions{})
	require.NoError(t, err)

	text := strings.Repeat("x", grpcDefaultReceiveLimitBytes+(2<<20))
	var echoed string
	require.NoError(t, integClient.InvokeRPC(ctx, flowID, messageSizeFlow{}.EchoText, text, &echoed))
	require.Equal(t, text, echoed)

	oversizedBytes := grpcDefaultReceiveLimitBytes + (1 << 20)
	textBytes := map[string]int{"a": oversizedBytes, "b": 1024, "c": 1024}
	for _, name := range messageSizeTextNames {
		require.NoError(t, integClient.InvokeRPC(ctx, flowID, messageSizeFlow{}.GenerateText,
			messageSizeTextInput{Name: name, Bytes: textBytes[name]}, nil))
	}
	requireMessageSizeOutputs(t, waitForFlow(t, flowID, true), textBytes)

	smallClient := newMessageSizeClient(t, grpcDefaultReceiveLimitBytes)
	_, err = smallClient.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{NeedsResults: true})
	var tooLarge *dex.BlobMessageTooLargeError
	require.ErrorAs(t, err, &tooLarge)
	require.Equal(t, flowID, tooLarge.FlowID)
	require.NotEmpty(t, tooLarge.BlobID)
}

func TestMessageSizeDefaultClientSplitsBatchAboveServerSendLimit(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "message-size-above-server-limit")
	_, err := integClient.StartFlow(ctx, messageSizeFlow{}, flowID, nil, dex.StartFlowOptions{})
	require.NoError(t, err)
	for _, name := range messageSizeTextNames {
		require.NoError(t, integClient.InvokeRPC(ctx, flowID, messageSizeFlow{}.WriteText,
			messageSizeTextInput{Name: name, Bytes: aboveServerLimitTextBytes}, nil))
	}

	texts := make(map[string]*string, len(messageSizeTextNames))
	reads := make([]dex.AttributeRead, 0, len(messageSizeTextNames))
	for _, name := range messageSizeTextNames {
		texts[name] = new(string)
		reads = append(reads, dex.ReadAttribute(messageSizeTexts[name], texts[name]))
	}
	_, err = newMessageSizeClient(t, 0).GetAttributes(ctx, flowID, reads...)
	require.NoError(t, err)
	for _, name := range messageSizeTextNames {
		require.Equal(t, strings.Repeat(name, aboveServerLimitTextBytes), *texts[name])
	}
	require.NoError(t, integClient.StopFlow(ctx, flowID, dex.StopOptions{}))
}

func TestMessageSizeDefaultWorkerSplitsBatchAboveServerSendLimit(t *testing.T) {
	ctx := integrationContext(t)
	flow := messageSizeUncachedFlow{}
	registry, err := dex.NewRegistry([]dex.Flow{flow})
	require.NoError(t, err)
	startUncachedMessageSizeWorker(ctx, t, registry)
	client, err := dex.NewClient(registry, newUncachedMessageSizeCache(t), dex.ClientOptions{
		FlowServiceAddress: flowServiceAddress(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	flowID := newFlowID(t, "message-size-worker-above-server-limit")
	_, err = client.StartFlow(ctx, flow, flowID, nil, dex.StartFlowOptions{})
	require.NoError(t, err)
	for _, name := range messageSizeTextNames {
		require.NoError(t, client.InvokeRPC(ctx, flowID, flow.WriteText,
			messageSizeTextInput{Name: name, Bytes: aboveServerLimitTextBytes}, nil))
	}

	var total int
	require.NoError(t, client.InvokeRPC(ctx, flowID, flow.SumTextLengths, nil, &total))
	require.Equal(t, len(messageSizeTextNames)*aboveServerLimitTextBytes, total)
	require.NoError(t, client.StopFlow(ctx, flowID, dex.StopOptions{}))
}

func requireMessageSizeOutputs(t *testing.T, result dex.FlowResult, textBytes map[string]int) {
	t.Helper()
	require.Equal(t, dex.FlowCompleted, result.Status)
	require.Len(t, result.Completions, len(textBytes))
	for _, completion := range result.Completions {
		var text string
		require.NoError(t, completion.Output.Decode(&text))
		require.NotEmpty(t, text)
		name := text[:1]
		require.Equal(t, strings.Repeat(name, textBytes[name]), text)
	}
}

// The cache rejects every hydrated text, so each read issues LoadBlobs.
func newUncachedMessageSizeCache(t *testing.T) *blobcache.Cache {
	t.Helper()
	cacheDir := t.TempDir()
	cache, err := blobcache.New(&blobcache.Config{Dir: cacheDir, MaxBytes: 1 << 10})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close()) })
	return cache
}

func startUncachedMessageSizeWorker(ctx context.Context, t *testing.T, registry *dex.Registry) {
	t.Helper()
	worker, err := dex.NewWorker(registry, newUncachedMessageSizeCache(t), dex.WorkerOptions{
		FlowServiceAddress: flowServiceAddress(),
		Pull:               &dex.PullWorkerOptions{AppWorkerID: "message-size-" + uuid.NewString()},
	})
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() { result <- worker.Start() }()
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, worker.Stop(stopCtx))
		require.NoError(t, <-result)
	})
	require.NoError(t, worker.WaitPullRegistered(ctx))
}

func newMessageSizeRegistry(t *testing.T) *dex.Registry {
	t.Helper()
	registry, err := dex.NewRegistry([]dex.Flow{messageSizeFlow{}})
	require.NoError(t, err)
	return registry
}

func newMessageSizeClient(t *testing.T, limitBytes int) *dex.Client {
	t.Helper()
	client, err := dex.NewClient(newMessageSizeRegistry(t), newUncachedMessageSizeCache(t), dex.ClientOptions{
		FlowServiceAddress:  flowServiceAddress(),
		GrpcMaxMessageBytes: limitBytes,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return client
}
