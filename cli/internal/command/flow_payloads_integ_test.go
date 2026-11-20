// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package command

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/superdurable/dex/cli/internal/payloadcodec"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// encryptedFlowService serves stored values that a Go SDK application encrypted with a shared key file.
type encryptedFlowService struct {
	*testFlowService
	result     *dexpb.FlowResult
	blobs      map[string]*dexpb.Value
	attributes []*dexpb.KV
}

func (s *encryptedFlowService) WaitForFlow(context.Context, *dexpb.WaitForFlowRequest) (*dexpb.FlowResult, error) {
	return s.result, nil
}

func (s *encryptedFlowService) LoadBlobs(context.Context, *dexpb.LoadBlobsRequest) (*dexpb.LoadBlobsResponse, error) {
	return &dexpb.LoadBlobsResponse{Values: s.blobs}, nil
}

func (s *encryptedFlowService) GetFlowState(context.Context, *dexpb.GetFlowStateRequest) (*dexpb.GetFlowStateResponse, error) {
	return &dexpb.GetFlowStateResponse{Attributes: s.attributes}, nil
}

func (s *encryptedFlowService) GetAttributes(context.Context, *dexpb.GetAttributesRequest) (*dexpb.GetAttributesResponse, error) {
	return &dexpb.GetAttributesResponse{Attributes: s.attributes}, nil
}

func startEncryptedFlowService(t *testing.T, service *encryptedFlowService) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	dexpb.RegisterFlowServiceServer(server, service)
	serveFinished := make(chan error, 1)
	go func() { serveFinished <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.GracefulStop()
		if err := <-serveFinished; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Errorf("serve FlowService: %v", err)
		}
	})
	return listener.Addr().String()
}

type sdkPayloadEncrypter struct {
	t     *testing.T
	codec dex.PayloadCodec
}

func newSDKPayloadEncrypter(t *testing.T, keyFilePath string) sdkPayloadEncrypter {
	t.Helper()
	keys, err := dex.NewFilePayloadEncryptionKeyProvider(keyFilePath)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := dex.NewAESGCMPayloadCodec(keys, dex.AESGCMPayloadCodecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return sdkPayloadEncrypter{t: t, codec: codec}
}

// envelope returns value encrypted the way the Go SDK stores it: a dex/1 object.
func (e sdkPayloadEncrypter) envelope(value *dexpb.Value) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
		Encoding: payloadcodec.EnvelopeMarker, Payload: e.envelopeBytes(value),
	}}}
}

func (e sdkPayloadEncrypter) text(text string) string {
	return payloadcodec.EncodedTextPrefix + base64.StdEncoding.EncodeToString(
		e.envelopeBytes(&dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: text}}),
	)
}

func (e sdkPayloadEncrypter) envelopeBytes(value *dexpb.Value) []byte {
	plaintext, err := proto.Marshal(value)
	if err != nil {
		e.t.Fatal(err)
	}
	envelopes, err := e.codec.Encode(context.Background(), dex.PayloadContext{FlowID: "flow-1"}, [][]byte{plaintext})
	if err != nil {
		e.t.Fatal(err)
	}
	envelopeBytes, err := proto.Marshal(envelopes[0])
	if err != nil {
		e.t.Fatal(err)
	}
	return envelopeBytes
}

func (e sdkPayloadEncrypter) open(value *dexpb.Value) *dexpb.Value {
	e.t.Helper()
	if value.GetObjValue().GetEncoding() != payloadcodec.EnvelopeMarker {
		e.t.Fatalf("value %v is not encrypted", value)
	}
	return e.openBytes(value.GetObjValue().GetPayload())
}

func (e sdkPayloadEncrypter) openText(text string) string {
	e.t.Helper()
	encoded, found := strings.CutPrefix(text, payloadcodec.EncodedTextPrefix)
	if !found {
		e.t.Fatalf("text %q is not encrypted", text)
	}
	envelopeBytes, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.openBytes(envelopeBytes).GetStringValue()
}

func (e sdkPayloadEncrypter) openBytes(envelopeBytes []byte) *dexpb.Value {
	envelope := &dexpb.PayloadEnvelope{}
	if err := proto.Unmarshal(envelopeBytes, envelope); err != nil {
		e.t.Fatal(err)
	}
	plaintexts, err := e.codec.Decode(context.Background(), dex.PayloadContext{FlowID: "flow-1"}, []*dexpb.PayloadEnvelope{envelope})
	if err != nil {
		e.t.Fatal(err)
	}
	value := &dexpb.Value{}
	if err := proto.Unmarshal(plaintexts[0], value); err != nil {
		e.t.Fatal(err)
	}
	return value
}

func generateTestKeyFile(t *testing.T) string {
	t.Helper()
	keyFilePath := filepath.Join(t.TempDir(), "key.json")
	if err := payloadcodec.GenerateKeyFile(keyFilePath, "k1", false); err != nil {
		t.Fatal(err)
	}
	return keyFilePath
}

func TestFlowWaitDecryptsStoredValuesAndErrorText(t *testing.T) {
	keyFilePath := generateTestKeyFile(t)
	encrypter := newSDKPayloadEncrypter(t, keyFilePath)
	service := &encryptedFlowService{
		testFlowService: &testFlowService{},
		result: &dexpb.FlowResult{
			FlowStatus:   dexpb.FlowStatus_FLOW_STATUS_FAILED,
			ErrorMessage: encrypter.text("card declined for order A-1"),
			Results: []*dexpb.StepCompletionOutput{
				{CompletedStepType: "Charge", CompletedStepOutput: encrypter.envelope(&dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "receipt-9"}})},
				{CompletedStepType: "Ship", CompletedStepOutput: &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForObjValue{InternalBlobIdForObjValue: "blob-9"}}},
			},
		},
		blobs: map[string]*dexpb.Value{
			"blob-9": encrypter.envelope(&dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "json", Payload: []byte(`{"carrier":"ups"}`)}}}),
		},
	}
	address := startEncryptedFlowService(t, service)

	decrypted := executeTestCommand(t, nil, "flow", "wait", "flow-1", "--server", address, "--codec-key-file", keyFilePath)
	output, err := json.Marshal(decrypted)
	if err != nil {
		t.Fatal(err)
	}
	for _, plaintext := range []string{`"errorMessage":"card declined for order A-1"`, `"completedStepOutput":"receipt-9"`, `"carrier":"ups"`} {
		if !bytes.Contains(output, []byte(plaintext)) {
			t.Fatalf("decrypted output misses %s: %s", plaintext, output)
		}
	}
	if _, hasWarnings := decrypted["warnings"]; hasWarnings {
		t.Fatalf("unexpected warnings: %s", output)
	}

	undecrypted := executeTestCommand(t, nil, "flow", "wait", "flow-1", "--server", address)
	output, err = json.Marshal(undecrypted)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output, []byte("receipt-9")) || !bytes.Contains(output, []byte(`"__dexEncryptedValue":{"keyId":"k1"}`)) ||
		!strings.Contains(string(output), "pass --codec-key-file or --codec-endpoint") {
		t.Fatalf("output without a codec: %s", output)
	}
}

func TestFlowWritesEncryptValuesAndReasons(t *testing.T) {
	keyFilePath := generateTestKeyFile(t)
	encrypter := newSDKPayloadEncrypter(t, keyFilePath)
	service := &encryptedFlowService{
		testFlowService: &testFlowService{},
		attributes: []*dexpb.KV{
			{Key: "secret", Value: encrypter.envelope(&dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "old"}})},
			{Key: "status", Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "open"}}},
		},
	}
	address := startEncryptedFlowService(t, service)
	codecFlags := []string{"--server", address, "--codec-key-file", keyFilePath}

	executeTestCommand(t, nil, append([]string{
		"flow", "start", "flow-1", "--flow-type", "OrderFlow", "--input", `{"order":"A-1"}`,
		"--attributes", `[{"key":"status","value":"new"}]`, "--yes",
	}, codecFlags...)...)
	start := service.startRequest
	if string(encrypter.open(start.GetStepInput()).GetObjValue().GetPayload()) != `{"order":"A-1"}` {
		t.Fatalf("start input = %v", start.GetStepInput())
	}
	if start.GetFlowStartOptions().GetAttributes()[0].GetValue().GetStringValue() != "new" {
		t.Fatalf("a new initial Attribute was encrypted: %v", start.GetFlowStartOptions().GetAttributes())
	}

	executeTestCommand(t, nil, append([]string{
		"flow", "stop", "flow-1", "--type", "force-complete", "--reason", "operator closed it", "--result", `"done"`, "--yes",
	}, codecFlags...)...)
	stop := service.stopRequest
	if encrypter.openText(stop.GetReason()) != "operator closed it" || encrypter.open(stop.GetOutput()).GetStringValue() != "done" {
		t.Fatalf("stop request = %v", stop)
	}

	executeTestCommand(t, nil, append([]string{
		"flow", "time-travel", "flow-1", "--type", "beginning", "--reason", "replay", "--yes",
	}, codecFlags...)...)
	if encrypter.openText(service.timeTravelRequest.GetReason()) != "replay" {
		t.Fatalf("time travel reason = %q", service.timeTravelRequest.GetReason())
	}

	executeTestCommand(t, nil, append([]string{
		"flow", "clone-fork", "flow-1", "--flow-id", "flow-2", "--reason", "try again",
		"--attributes", `[{"key":"secret","value":"new"},{"key":"status","value":"closed"},{"key":"added","value":1}]`, "--yes",
	}, codecFlags...)...)
	cloneFork := service.cloneForkRequest
	if encrypter.openText(cloneFork.GetReason()) != "try again" {
		t.Fatalf("clone-fork reason = %q", cloneFork.GetReason())
	}
	written := make(map[string]*dexpb.Value)
	for _, attribute := range cloneFork.GetAttributes() {
		written[attribute.GetKey()] = attribute.GetValue()
	}
	if encrypter.open(written["secret"]).GetStringValue() != "new" ||
		written["status"].GetStringValue() != "closed" || written["added"].GetIntValue() != 1 {
		t.Fatalf("clone-fork Attributes = %v", cloneFork.GetAttributes())
	}
}

func TestFlowStateKeepsApplicationTextThatLooksEncoded(t *testing.T) {
	keyFilePath := generateTestKeyFile(t)
	service := &encryptedFlowService{
		testFlowService: &testFlowService{},
		attributes: []*dexpb.KV{{
			Key:   "dex-encoded:attribute-key",
			Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "dex-encoded:application value"}},
		}},
	}
	address := startEncryptedFlowService(t, service)
	state := executeTestCommand(t, nil, "flow", "state", "dex-encoded:flow-id", "--server", address, "--codec-key-file", keyFilePath)
	output, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output, []byte(`"key":"dex-encoded:attribute-key"`)) ||
		!bytes.Contains(output, []byte(`"dex-encoded:application value"`)) || bytes.Contains(output, []byte("warnings")) {
		t.Fatalf("flow state changed application text: %s", output)
	}
}

func TestFlowReasonWithReservedPrefixGetsSpaceWithoutCodec(t *testing.T) {
	service, address := startTestFlowService(t)
	executeTestCommand(t, nil, "flow", "stop", "flow-1", "--reason", "dex-encoded:not encrypted", "--yes", "--server", address)
	service.mu.Lock()
	reason := service.stopRequest.GetReason()
	service.mu.Unlock()
	if reason != " dex-encoded:not encrypted" {
		t.Fatalf("reason = %q", reason)
	}
}
