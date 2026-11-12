// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package remotesession

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/internal/pythonjson"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

// conformanceDir holds the contract files that the Python core generates.
var conformanceDir = filepath.Join("..", "..", "..", "conformance")

// The Go types of the conformance suite's RPC input and output type names.
var conformanceGoTypes = map[string]reflect.Type{
	"AgentUserMessage":             reflect.TypeFor[dexagents.AgentUserMessage](),
	"SteerUserMessageRequest":      reflect.TypeFor[dexagents.SteerUserMessageRequest](),
	"DeleteQueuedMessageRequest":   reflect.TypeFor[dexagents.DeleteQueuedMessageRequest](),
	"CancelTurnRequest":            reflect.TypeFor[dexagents.CancelTurnRequest](),
	"ResolveInterruptRequest":      reflect.TypeFor[dexagents.ResolveInterruptRequest](),
	"AnswerQuestionRequest":        reflect.TypeFor[dexagents.AnswerQuestionRequest](),
	"ExecutePlanRequest":           reflect.TypeFor[dexagents.ExecutePlanRequest](),
	"UpdateSessionSettingsRequest": reflect.TypeFor[dexagents.UpdateSessionSettingsRequest](),
	"ListAgentMessagesRequest":     reflect.TypeFor[dexagents.ListAgentMessagesRequest](),
	"AgentSnapshot":                reflect.TypeFor[dexagents.AgentSnapshot](),
	"AgentMessagesPage":            reflect.TypeFor[dexagents.AgentMessagesPage](),
	"bool":                         reflect.TypeFor[bool](),
}

// A fast precheck against names.json; the cross-language tests in ../integ are the real proof.
func TestSessionRPCContractsMatchTheConformanceSuite(t *testing.T) {
	text, err := os.ReadFile(filepath.Join(conformanceDir, "names.json"))
	require.NoError(t, err)
	var names struct {
		RPCs []struct {
			Name              string   `json:"name"`
			Input             *string  `json:"input"`
			Output            string   `json:"output"`
			IsTransactional   bool     `json:"is_transactional"`
			Locks             []string `json:"locks"`
			LoadAttributeMaps []string `json:"load_attribute_maps"`
			LoadChannels      []string `json:"load_channels"`
		} `json:"rpcs"`
		AgentEventsDefaultCapacityBytes int64 `json:"agent_events_default_capacity_bytes"`
	}
	require.NoError(t, json.Unmarshal(text, &names))
	require.Equal(t, names.AgentEventsDefaultCapacityBytes, dexagents.DefaultAgentEventsCapacityBytes)
	require.Len(t, names.RPCs, len(sessionRPCContracts), "every contract RPC and no other")

	contractsByName := map[string]sessionRPCContract{}
	for _, contract := range sessionRPCContracts {
		contractsByName[contract.name] = contract
	}
	flowType := reflect.TypeFor[*RemoteSessionFlow]()
	for _, expected := range names.RPCs {
		contract, isKnown := contractsByName[expected.Name]
		require.True(t, isKnown, expected.Name)
		require.Equal(t, expected.IsTransactional, contract.isTransactional, expected.Name)
		require.Equal(t, expected.Locks, sortedNames(contract.lockedAttributes, namedAttributeLock.attributeName), expected.Name)
		require.Equal(t, expected.LoadAttributeMaps,
			sortedNames(contract.loadedAttributeMaps, namedAttributeDef.attributeName), expected.Name)
		require.Equal(t, expected.LoadChannels, sortedNames(contract.loadedChannels, namedChannelDef.channelName), expected.Name)

		method, hasMethod := flowType.MethodByName(contract.goMethodName)
		require.True(t, hasMethod, contract.goMethodName)
		inputType := method.Type.In(2)
		if expected.Input == nil {
			require.Equal(t, reflect.TypeFor[dex.None](), inputType, expected.Name)
		} else {
			require.Equal(t, conformanceGoTypes[*expected.Input], inputType, expected.Name)
		}
		outputType := method.Type.Out(0).Elem().Field(0).Type
		require.Equal(t, conformanceGoTypes[expected.Output], outputType, expected.Name)
	}
}

// Each RPC input and output type decodes the suite's golden wire values and re-encodes them canonically.
func TestSessionRPCValuesMatchTheConformanceSuite(t *testing.T) {
	for typeName, goType := range conformanceGoTypes {
		if typeName == "bool" {
			continue
		}
		text, err := os.ReadFile(filepath.Join(conformanceDir, "values", typeName+".json"))
		require.NoError(t, err, typeName)
		var golden struct {
			Cases []struct {
				Name      string `json:"name"`
				Wire      string `json:"wire"`
				Canonical string `json:"canonical"`
			} `json:"cases"`
		}
		require.NoError(t, json.Unmarshal(text, &golden), typeName)
		require.NotEmpty(t, golden.Cases, typeName)
		for _, goldenCase := range golden.Cases {
			value := reflect.New(goType).Interface()
			require.NoError(t, json.Unmarshal([]byte(goldenCase.Wire), value), typeName+"/"+goldenCase.Name)
			encoded, err := json.Marshal(value)
			require.NoError(t, err, typeName+"/"+goldenCase.Name)
			canonical, err := pythonjson.Marshal(json.RawMessage(encoded),
				pythonjson.Options{IsCompact: true, ShouldSortKeys: true, ShouldEscapeNonASCII: true})
			require.NoError(t, err, typeName+"/"+goldenCase.Name)
			require.Equal(t, goldenCase.Canonical, canonical, typeName+"/"+goldenCase.Name)
		}
	}
}

func TestNewRequestTypesRejectMissingRequiredFields(t *testing.T) {
	for _, testCase := range []struct {
		wire  string
		value any
	}{
		{`{}`, &dexagents.ExecutePlanRequest{}},
		{`{"message_id":null}`, &dexagents.SteerUserMessageRequest{}},
		{`{"text":null}`, &dexagents.SteerUserMessageRequest{}},
		{`{}`, &dexagents.DeleteQueuedMessageRequest{}},
		{`{"interrupt_id":"call-1"}`, &dexagents.AnswerQuestionRequest{}},
		{`null`, &dexagents.CancelTurnRequest{}},
	} {
		require.Error(t, json.Unmarshal([]byte(testCase.wire), testCase.value), testCase.wire)
	}
	var cancel dexagents.CancelTurnRequest
	require.NoError(t, json.Unmarshal([]byte(`{}`), &cancel))
	require.Nil(t, cancel.TurnID)
	encoded, err := json.Marshal(dexagents.SteerUserMessageRequest{})
	require.NoError(t, err)
	require.JSONEq(t, `{"message_id":null,"text":null}`, string(encoded), "null fields are sent, never omitted")
}

// A Client registers the Flow; a Worker refuses it, so a Go Worker never polls RPCs it cannot run.
func TestRemoteSessionFlowServesClientsOnly(t *testing.T) {
	flow, err := NewRemoteSessionFlow("SupportAgent")
	require.NoError(t, err)
	require.True(t, dex.IsClientOnlyFlow(flow))
	require.Empty(t, flow.GetSteps())
	require.Equal(t, "SupportAgent", flow.GetFlowType())
	require.Equal(t, dexagents.AgentEventsStreamName, flow.AgentEvents().StreamName())
	require.Equal(t, dexagents.DefaultAgentEventsCapacityBytes, flow.AgentEvents().StreamCapacityBytes())
	other, err := NewRemoteSessionFlow("TriageAgent")
	require.NoError(t, err)

	registry, err := dex.NewRegistry([]dex.Flow{flow, other})
	require.NoError(t, err, "two Flow types, each with its own AgentEvents Stream")
	cache, err := blobcache.New(&blobcache.Config{
		Dir: t.TempDir(), MaxBytes: 1 << 20, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close()) })
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{})
	require.NoError(t, err)
	require.NoError(t, client.Close())
	_, err = dex.NewWorker(registry, cache, dex.WorkerOptions{})
	var clientOnly *dex.ClientOnlyFlowError
	require.ErrorAs(t, err, &clientOnly)
	require.Equal(t, "SupportAgent", clientOnly.FlowType)

	_, err = flow.SendUserMessage(nil, dexagents.AgentUserMessage{Text: "hi"})
	require.EqualError(t, err, "remotesession: Flow type SupportAgent is client-only; its Workers serve RPC send_user_message")

	_, err = NewRemoteSessionFlow(" \t")
	require.Error(t, err)
}

func sortedNames[Named any](items []Named, name func(Named) string) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, name(item))
	}
	slices.Sort(names)
	return names
}

func (locked namedAttributeLock) attributeName() string { return locked.name }

func (loaded namedAttributeDef) attributeName() string { return loaded.name }

func (loaded namedChannelDef) channelName() string { return loaded.name }
