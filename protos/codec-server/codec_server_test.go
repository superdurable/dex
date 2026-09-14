// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package codecserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestExecuteStartsAndStopsCodecServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	shutdownContext, cancelShutdown := context.WithCancel(context.Background())
	defer cancelShutdown()
	resultChannel := make(chan error, 1)
	var stdout strings.Builder
	go func() {
		resultChannel <- Execute(shutdownContext, []string{"--address", address}, &stdout, io.Discard)
	}()

	httpClient := &http.Client{Timeout: 250 * time.Millisecond}
	require.Eventually(t, func() bool {
		response, requestErr := httpClient.Get("http://" + address + "/healthz")
		if requestErr != nil {
			return false
		}
		closeErr := response.Body.Close()
		return closeErr == nil && response.StatusCode == http.StatusOK
	}, 5*time.Second, 20*time.Millisecond)

	cancelShutdown()
	select {
	case executeErr := <-resultChannel:
		require.NoError(t, executeErr)
	case <-time.After(5 * time.Second):
		t.Fatal("codec server did not stop")
	}
	require.Contains(t, stdout.String(), "Temporal protobuf Codec Server listening")
	require.Contains(t, stdout.String(), "Temporal protobuf Codec Server stopped")
}

func TestCodecHTTPHandlerDecodesAndEncodesDexProtobuf(t *testing.T) {
	codecServerHandler := newCodecServerHTTPHandler()
	workflowInput := &dexpb.InterpreterWorkflowInput{
		FlowType: "order",
		StepInput: &dexpb.Value{
			Kind: &dexpb.Value_StringValue{StringValue: "hello"},
		},
	}
	binaryPayloadConverter := converter.NewProtoPayloadConverter()
	binaryPayload, err := binaryPayloadConverter.ToPayload(workflowInput)
	require.NoError(t, err)

	decodeResponse := postPayloads(t, codecServerHandler, "/decode", []*commonpb.Payload{binaryPayload}, "")
	require.Equal(t, http.StatusOK, decodeResponse.Code)
	decodedPayloads := unmarshalPayloadResponse(t, decodeResponse)
	require.Len(t, decodedPayloads, 1)
	decodedPayload := decodedPayloads[0]
	require.Equal(t, converter.MetadataEncodingProtoJSON, string(decodedPayload.Metadata[converter.MetadataEncoding]))
	require.Equal(t, "dex.InterpreterWorkflowInput", string(decodedPayload.Metadata[converter.MetadataMessageType]))

	var decodedJSON map[string]interface{}
	require.NoError(t, json.Unmarshal(decodedPayload.Data, &decodedJSON))
	require.Equal(t, "order", decodedJSON["flowType"])
	require.Equal(t, map[string]interface{}{"stringValue": "hello"}, decodedJSON["stepInput"])

	encodeResponse := postPayloads(t, codecServerHandler, "/encode", decodedPayloads, "")
	require.Equal(t, http.StatusOK, encodeResponse.Code)
	encodedPayloads := unmarshalPayloadResponse(t, encodeResponse)
	require.Len(t, encodedPayloads, 1)
	require.Equal(t, converter.MetadataEncodingProto, string(encodedPayloads[0].Metadata[converter.MetadataEncoding]))

	roundTripInput := &dexpb.InterpreterWorkflowInput{}
	require.NoError(t, binaryPayloadConverter.FromPayload(encodedPayloads[0], roundTripInput))
	require.True(t, proto.Equal(workflowInput, roundTripInput))
}

func TestCodecHTTPHandlerPassesThroughUnsupportedPayloads(t *testing.T) {
	codecServerHandler := newCodecServerHTTPHandler()
	jsonPayload, err := converter.GetDefaultDataConverter().ToPayload(map[string]string{"status": "ready"})
	require.NoError(t, err)
	unknownMessagePayload := &commonpb.Payload{
		Metadata: map[string][]byte{
			converter.MetadataEncoding:    []byte(converter.MetadataEncodingProto),
			converter.MetadataMessageType: []byte("dex.UnknownMessage"),
		},
		Data: []byte{1, 2, 3},
	}
	missingMessageTypePayload := &commonpb.Payload{
		Metadata: map[string][]byte{
			converter.MetadataEncoding: []byte(converter.MetadataEncodingProto),
		},
		Data: []byte{4, 5, 6},
	}

	response := postPayloads(
		t,
		codecServerHandler,
		"/decode",
		[]*commonpb.Payload{jsonPayload, unknownMessagePayload, missingMessageTypePayload},
		"",
	)
	require.Equal(t, http.StatusOK, response.Code)
	decodedPayloads := unmarshalPayloadResponse(t, response)
	require.Len(t, decodedPayloads, 3)
	require.True(t, proto.Equal(jsonPayload, decodedPayloads[0]))
	require.True(t, proto.Equal(unknownMessagePayload, decodedPayloads[1]))
	require.True(t, proto.Equal(missingMessageTypePayload, decodedPayloads[2]))

	unknownProtoJSONPayload := &commonpb.Payload{
		Metadata: map[string][]byte{
			converter.MetadataEncoding:    []byte(converter.MetadataEncodingProtoJSON),
			converter.MetadataMessageType: []byte("dex.UnknownMessage"),
		},
		Data: []byte(`{"status":"ready"}`),
	}
	encodeResponse := postPayloads(t, codecServerHandler, "/encode", []*commonpb.Payload{unknownProtoJSONPayload}, "")
	require.Equal(t, http.StatusOK, encodeResponse.Code)
	encodedPayloads := unmarshalPayloadResponse(t, encodeResponse)
	require.Len(t, encodedPayloads, 1)
	require.True(t, proto.Equal(unknownProtoJSONPayload, encodedPayloads[0]))
}

func TestCodecHTTPHandlerRejectsMalformedKnownProtobuf(t *testing.T) {
	codecServerHandler := newCodecServerHTTPHandler()
	malformedPayload := &commonpb.Payload{
		Metadata: map[string][]byte{
			converter.MetadataEncoding:    []byte(converter.MetadataEncodingProto),
			converter.MetadataMessageType: []byte("dex.InterpreterWorkflowInput"),
		},
		Data: []byte{0xff},
	}

	response := postPayloads(t, codecServerHandler, "/decode", []*commonpb.Payload{malformedPayload}, "")
	require.Equal(t, http.StatusBadRequest, response.Code)
}

func TestCodecHTTPHandlerRejectsMalformedKnownProtoJSON(t *testing.T) {
	codecServerHandler := newCodecServerHTTPHandler()
	malformedPayload := &commonpb.Payload{
		Metadata: map[string][]byte{
			converter.MetadataEncoding:    []byte(converter.MetadataEncodingProtoJSON),
			converter.MetadataMessageType: []byte("dex.InterpreterWorkflowInput"),
		},
		Data: []byte("{"),
	}

	response := postPayloads(t, codecServerHandler, "/encode", []*commonpb.Payload{malformedPayload}, "")
	require.Equal(t, http.StatusBadRequest, response.Code)
}

func TestCodecHTTPHandlerAllowsTemporalCloudCORS(t *testing.T) {
	request := httptest.NewRequest(http.MethodOptions, "/decode", nil)
	request.Header.Set("Origin", temporalCloudWebOrigin)
	request.Header.Set(corsRequestedMethodHeader, http.MethodPost)
	response := httptest.NewRecorder()

	newCodecServerHTTPHandler().ServeHTTP(response, request)

	require.Equal(t, http.StatusNoContent, response.Code)
	require.Equal(t, temporalCloudWebOrigin, response.Header().Get(corsAllowedOriginHeader))
	require.Equal(t, corsAllowedMethods, response.Header().Get(corsAllowedMethodsHeader))
	require.Equal(t, corsAllowedHeaders, response.Header().Get(corsAllowedHeadersHeader))
}

func TestCodecHTTPHandlerRejectsOtherBrowserOrigins(t *testing.T) {
	response := postPayloads(t, newCodecServerHTTPHandler(), "/decode", nil, "https://example.com")
	require.Equal(t, http.StatusForbidden, response.Code)
}

func TestCodecHTTPHandlerAllowsRequestsWithoutBrowserOrigin(t *testing.T) {
	response := postPayloads(t, newCodecServerHTTPHandler(), "/decode", nil, "")
	require.Equal(t, http.StatusOK, response.Code)
}

func TestHealthCheck(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	newCodecServerHTTPHandler().ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "ok\n", response.Body.String())
}

func TestValidateLoopbackAddress(t *testing.T) {
	require.NoError(t, validateLoopbackAddress("127.0.0.1:8804"))
	require.NoError(t, validateLoopbackAddress("[::1]:8804"))
	require.NoError(t, validateLoopbackAddress("localhost:8804"))
	require.Error(t, validateLoopbackAddress("0.0.0.0:8804"))
	require.Error(t, validateLoopbackAddress("192.0.2.1:8804"))
	require.Error(t, validateLoopbackAddress(":8804"))
}

func TestParseCodecServerConfigUsesDefaultAddress(t *testing.T) {
	codecServerConfig, err := parseCodecServerConfig(nil, io.Discard)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:8804", codecServerConfig.address)
}

func postPayloads(
	t *testing.T,
	codecServerHandler http.Handler,
	endpointPath string,
	payloads []*commonpb.Payload,
	origin string,
) *httptest.ResponseRecorder {
	t.Helper()
	requestBody, err := protojson.Marshal(&commonpb.Payloads{Payloads: payloads})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, endpointPath, bytes.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response := httptest.NewRecorder()
	codecServerHandler.ServeHTTP(response, request)
	return response
}

func unmarshalPayloadResponse(t *testing.T, response *httptest.ResponseRecorder) []*commonpb.Payload {
	t.Helper()
	payloads := &commonpb.Payloads{}
	require.NoError(t, protojson.Unmarshal(response.Body.Bytes(), payloads))
	return payloads.Payloads
}
