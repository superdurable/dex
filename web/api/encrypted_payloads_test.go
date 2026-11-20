// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

type encryptedWriteTestClient struct {
	dexpb.FlowServiceClient
	published  []*dexpb.PublishToChannelRequest
	attributes []*dexpb.SetAttributesRequest
	stops      []*dexpb.StopFlowRequest
}

func (c *encryptedWriteTestClient) PublishToChannel(
	_ context.Context, request *dexpb.PublishToChannelRequest, _ ...grpc.CallOption,
) (*emptypb.Empty, error) {
	c.published = append(c.published, request)
	return &emptypb.Empty{}, nil
}

func (c *encryptedWriteTestClient) SetAttributes(
	_ context.Context, request *dexpb.SetAttributesRequest, _ ...grpc.CallOption,
) (*emptypb.Empty, error) {
	c.attributes = append(c.attributes, request)
	return &emptypb.Empty{}, nil
}

func (c *encryptedWriteTestClient) StopFlow(
	_ context.Context, request *dexpb.StopFlowRequest, _ ...grpc.CallOption,
) (*emptypb.Empty, error) {
	c.stops = append(c.stops, request)
	return &emptypb.Empty{}, nil
}

// testEnvelopeBytes stands in for a serialized PayloadEnvelope; Dex Web never parses it.
var testEnvelopeBytes = []byte{0x08, 0x01, 0x12, 0x02, 'k', '1', 0x1a, 0x03, 1, 2, 3}

const testEncodedValueJSON = `{"objValue":{"encoding":"dex/1","payload":"CAESAmsxGgMBAgM="}}`

func performEncryptedWrite(client *encryptedWriteTestClient, method string, path string, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	RegisterHandlers(mux, client)
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func wantEnvelopeValue(t *testing.T, value *dexpb.Value) {
	t.Helper()
	want := &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
		Encoding: "dex/1", Payload: testEnvelopeBytes,
	}}}
	if !proto.Equal(value, want) {
		t.Fatalf("value = %v, want the encoded value unchanged", value)
	}
}

func TestEncodedChannelMessageAndAttributePassThroughUnchanged(t *testing.T) {
	client := &encryptedWriteTestClient{}
	response := performEncryptedWrite(client, http.MethodPost, "/api/flows/channels/messages",
		`{"flowId":"f1","channelName":"approvals","encodedValue":`+testEncodedValueJSON+`}`)
	if response.Code != http.StatusOK {
		t.Fatalf("publish status = %d: %s", response.Code, response.Body.String())
	}
	wantEnvelopeValue(t, client.published[0].GetMessages()[0].GetValue())

	response = performEncryptedWrite(client, http.MethodPut, "/api/flows/attributes",
		`{"flowId":"f1","key":"note","encodedValue":`+testEncodedValueJSON+`}`)
	if response.Code != http.StatusOK {
		t.Fatalf("set attribute status = %d: %s", response.Code, response.Body.String())
	}
	wantEnvelopeValue(t, client.attributes[0].GetAttributes()[0].GetValue())
}

func TestEncodedValueRejectsMixedFormsAndOtherEncodings(t *testing.T) {
	for name, body := range map[string]string{
		"both forms":     `{"flowId":"f1","key":"k","valueType":"string","value":"x","encodedValue":` + testEncodedValueJSON + `}`,
		"json encoding":  `{"flowId":"f1","key":"k","encodedValue":{"objValue":{"encoding":"json","payload":"e30="}}}`,
		"string value":   `{"flowId":"f1","key":"k","encodedValue":{"stringValue":"plain"}}`,
		"empty payload":  `{"flowId":"f1","key":"k","encodedValue":{"objValue":{"encoding":"dex/1"}}}`,
		"not Value JSON": `{"flowId":"f1","key":"k","encodedValue":{"unknown":1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			client := &encryptedWriteTestClient{}
			response := performEncryptedWrite(client, http.MethodPut, "/api/flows/attributes", body)
			if response.Code != http.StatusBadRequest || len(client.attributes) != 0 {
				t.Fatalf("status = %d, writes = %d; want 400 and no write", response.Code, len(client.attributes))
			}
		})
	}
}

func TestStopPassesEncodedOutputAndEncodedReasonThrough(t *testing.T) {
	client := &encryptedWriteTestClient{}
	response := performEncryptedWrite(client, http.MethodPost, "/api/flows/stop",
		`{"flowId":"f1","stopType":4,"encodedOutput":`+testEncodedValueJSON+`}`)
	if response.Code != http.StatusOK {
		t.Fatalf("stop status = %d: %s", response.Code, response.Body.String())
	}
	wantEnvelopeValue(t, client.stops[0].GetOutput())

	response = performEncryptedWrite(client, http.MethodPost, "/api/flows/stop",
		`{"flowId":"f1","stopType":3,"reason":"dex-encoded:CAESAmsxGgMBAgM="}`)
	if response.Code != http.StatusOK || client.stops[1].GetReason() != "dex-encoded:CAESAmsxGgMBAgM=" {
		t.Fatalf("stop status = %d, reason = %q; want the encoded reason unchanged", response.Code, client.stops[1].GetReason())
	}

	response = performEncryptedWrite(client, http.MethodPost, "/api/flows/stop",
		`{"flowId":"f1","stopType":2,"reason":"r","encodedOutput":`+testEncodedValueJSON+`}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("terminate with encodedOutput status = %d, want 400", response.Code)
	}
}

func TestStartAcceptsEncodedInputOnlyAlone(t *testing.T) {
	objectSchema := V2StartInputSchema{Kind: "object"}
	value, err := v2StartRequestStepInput(objectSchema, v2StartRequest{EncodedInput: json.RawMessage(testEncodedValueJSON)})
	if err != nil {
		t.Fatal(err)
	}
	wantEnvelopeValue(t, value)
	if _, err := v2StartRequestStepInput(objectSchema, v2StartRequest{
		Input: json.RawMessage(`{}`), EncodedInput: json.RawMessage(testEncodedValueJSON),
	}); err == nil {
		t.Fatal("input with encodedInput was accepted")
	}
	if _, err := v2StartRequestStepInput(V2StartInputSchema{Kind: "null"}, v2StartRequest{
		EncodedInput: json.RawMessage(testEncodedValueJSON),
	}); err == nil {
		t.Fatal("an encoded null Start input was accepted")
	}
}

type blobAttributeTestClient struct {
	dexpb.FlowServiceClient
	blobs map[string]*dexpb.Value
}

func (c *blobAttributeTestClient) LoadBlobs(
	_ context.Context, request *dexpb.LoadBlobsRequest, _ ...grpc.CallOption,
) (*dexpb.LoadBlobsResponse, error) {
	values := make(map[string]*dexpb.Value)
	for _, entry := range request.GetEntries() {
		id := v2BlobID(entry.GetBlobValue())
		if value, ok := c.blobs[id]; ok {
			values[id] = value
		}
	}
	return &dexpb.LoadBlobsResponse{Values: values}, nil
}

func TestActionChecksLoadBlobOffloadedEncryptedAttributes(t *testing.T) {
	envelope := &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
		Encoding: "dex/1", Payload: testEnvelopeBytes,
	}}}
	handler := &v2Handler{client: &blobAttributeTestClient{blobs: map[string]*dexpb.Value{"b1": envelope}}}
	snapshot := handler.hydratedKeyValueMap(context.Background(), "f1", []*dexpb.KV{
		{Key: "owner", Value: &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForObjValue{InternalBlobIdForObjValue: "b1"}}},
		{Key: "lost", Value: &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForObjValue{InternalBlobIdForObjValue: "missing"}}},
	})
	if err := actionConditionValueError("owner", snapshot["owner"]); err == nil || !strings.Contains(err.Error(), "is encrypted") {
		t.Fatalf("condition error = %v, want an encrypted Attribute error", err)
	}
	if err := actionConditionValueError("lost", snapshot["lost"]); err == nil || !strings.Contains(err.Error(), "could not be loaded") {
		t.Fatalf("condition error = %v, want a load error", err)
	}
	action := V2Action{RPCName: "Approve", Input: V2ActionInput{Kind: "object", Fields: []V2ActionInputField{{
		FieldName: "owner", Source: "attribute", AttributeKey: "lost", ValueType: "json", Required: true,
	}}}}
	if _, err := buildActionInput(action, nil, snapshot); err == nil || !strings.Contains(err.Error(), "could not be loaded") {
		t.Fatalf("input error = %v, want the blob reference refused", err)
	}
}

func TestEncryptedValuesAreMarkedInDexWebJSON(t *testing.T) {
	envelope := &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
		Encoding: "dex/1", Payload: testEnvelopeBytes,
	}}}
	want := map[string]interface{}{"__dexEnvelope": map[string]interface{}{"encoding": "dex/1", "payload": "CAESAmsxGgMBAgM="}}
	if got := dexValue(envelope); !reflect.DeepEqual(got, want) {
		t.Fatalf("dexValue = %v, want %v", got, want)
	}
	history, err := protoMap(&dexpb.KV{Key: "k", Value: envelope})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(history["value"], want) {
		t.Fatalf("protoMap value = %v, want %v", history["value"], want)
	}
	// Application JSON that looks like an envelope stays application data.
	lookalike := &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
		Encoding: "json", Payload: []byte(`{"encoding":"dex/1","payload":"CAESAmsxGgMBAgM="}`),
	}}}
	if isEncryptedMappedValue(dexValue(lookalike)) {
		t.Fatal("application JSON was marked as an encrypted value")
	}
}

func TestApplicationJSONLookalikeMarkersAreEscapedAndStayApplicationData(t *testing.T) {
	const envelopeLookalike = `{"__dexEnvelope":{"encoding":"dex/1","payload":"CAESAmsxGgMBAgM="}}`
	const blobLookalike = `{"__dexBlobReference":{"id":"b9","kind":"object"}}`
	jsonValue := func(text string) *dexpb.Value {
		return &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "json", Payload: []byte(text)}}}
	}
	for name, text := range map[string]string{"envelope": envelopeLookalike, "blob reference": blobLookalike} {
		t.Run(name, func(t *testing.T) {
			var original interface{}
			if err := json.Unmarshal([]byte(text), &original); err != nil {
				t.Fatal(err)
			}
			inline := dexValue(jsonValue(text))
			if isEncryptedMappedValue(inline) || isBlobReferenceMappedValue(inline) {
				t.Fatalf("inline application JSON %v reads as a Dex Web marker", inline)
			}
			if _, escaped := inline.(map[string]interface{})[escapedObjectField]; !escaped {
				t.Fatalf("inline application JSON %v is not escaped", inline)
			}
			if !reflect.DeepEqual(unescapeApplicationJSON(inline), original) {
				t.Fatalf("unescaped %v, want %v", unescapeApplicationJSON(inline), original)
			}

			handler := &v2Handler{client: &blobAttributeTestClient{blobs: map[string]*dexpb.Value{"b1": jsonValue(text)}}}
			snapshot := handler.hydratedKeyValueMap(context.Background(), "f1", []*dexpb.KV{
				{Key: "owner", Value: &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForObjValue{InternalBlobIdForObjValue: "b1"}}},
			})
			if err := actionConditionValueError("owner", snapshot["owner"]); err != nil {
				t.Fatalf("blob-loaded application JSON gave condition error %v", err)
			}
			condition := V2ActionCondition{AttributeKey: "owner", Operator: "in", Values: []interface{}{original}}
			if !actionConditionMatches(condition, snapshot["owner"]) {
				t.Fatal("the condition does not match the application's own JSON")
			}
			action := V2Action{RPCName: "Approve", Input: V2ActionInput{Kind: "object", Fields: []V2ActionInputField{{
				FieldName: "owner", Source: "attribute", AttributeKey: "owner", ValueType: "json", Required: true,
			}}}}
			input, err := buildActionInput(action, nil, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			var sent map[string]interface{}
			if err := json.Unmarshal(input.GetObjValue().GetPayload(), &sent); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(sent["owner"], original) {
				t.Fatalf("Action input sent %v, want the application JSON %v", sent["owner"], original)
			}
		})
	}
}

func TestEncryptedStatusMessageIsPassedForTheBrowserOnlyWithAServiceErrorResponse(t *testing.T) {
	const encoded = "dex-encoded:CAESAmsxGgMBAgM="
	withDetail, err := status.New(codes.FailedPrecondition, encoded).WithDetails(&dexpb.ServiceErrorResponse{Detail: encoded})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	writeGRPCError(recorder, withDetail.Err(), "Approve")
	var body errorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.EncodedError != encoded || body.Error != encryptedErrorMessage {
		t.Fatalf("response = %+v, want the encoded message passed for the browser", body)
	}

	recorder = httptest.NewRecorder()
	writeGRPCError(recorder, status.Error(codes.FailedPrecondition, encoded), "Approve")
	body = errorResponse{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.EncodedError != "" || body.Error != encoded {
		t.Fatalf("response = %+v, want a status without ServiceErrorResponse unchanged", body)
	}
}

func TestActionInputFromAnEncryptedAttributeFailsClearly(t *testing.T) {
	action := V2Action{
		RPCName: "Approve",
		Input: V2ActionInput{Kind: "object", Fields: []V2ActionInputField{{
			FieldName: "owner", Source: "attribute", AttributeKey: "owner", ValueType: "string", Required: true,
		}}},
	}
	snapshot := map[string]interface{}{"owner": mappedEncryptedEnvelope("dex/1", "CAESAmsxGgMBAgM=")}
	_, err := buildActionInput(action, nil, snapshot)
	if err == nil || !strings.Contains(err.Error(), "is encrypted") {
		t.Fatalf("error = %v, want an encrypted Attribute error", err)
	}
	if !isEncryptedValue(&dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "dex/1"}}}) {
		t.Fatal("a dex/1 Value is not reported as encrypted")
	}
	if isEncryptedMappedValue(map[string]interface{}{"encoding": "json", "payload": "e30="}) {
		t.Fatal("a json object is reported as encrypted")
	}
}
