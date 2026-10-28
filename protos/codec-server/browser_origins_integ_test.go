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
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestCodecServerBrowserOrigins(t *testing.T) {
	codecHTTPServer := httptest.NewServer(newCodecServerHTTPHandler())
	t.Cleanup(codecHTTPServer.Close)

	for _, originCase := range []struct {
		origin    string
		isAllowed bool
	}{
		{origin: temporalCloudWebOrigin, isAllowed: true},
		{origin: "http://localhost:8233", isAllowed: true},
		{origin: "http://localhost:28233", isAllowed: true},
		{origin: "http://localhost", isAllowed: true},
		{origin: "https://localhost:8233", isAllowed: true},
		{origin: "http://127.0.0.1:28233", isAllowed: true},
		{origin: "http://127.0.0.2:8233", isAllowed: true},
		{origin: "https://127.0.0.1:8233", isAllowed: true},
		{origin: "http://[::1]:8233", isAllowed: true},
		{origin: "https://[::1]:28233", isAllowed: true},
		{origin: "", isAllowed: true},
		{origin: "https://example.com"},
		{origin: "http://192.168.1.10:8233"},
		{origin: "http://0.0.0.0:8233"},
		{origin: "http://[::]:8233"},
		{origin: "http://localhost.example.com:8233"},
		{origin: "http://127.0.0.1.example.com:8233"},
		{origin: "http://localhost@evil.example:8233"},
		{origin: "http://evil.example@localhost:8233"},
		{origin: "http://localhost:8233/path"},
		{origin: "http://localhost:8233/"},
		{origin: "http://localhost:8233?query=value"},
		{origin: "http://localhost:8233?"},
		{origin: "http://localhost:8233#fragment"},
		{origin: "http://localhost:8233#"},
		{origin: "ftp://localhost:8233"},
		{origin: "http://localhost:invalid"},
		{origin: "null"},
		{origin: "http://localhost:8233 https://evil.example"},
	} {
		t.Run(originCase.origin, func(t *testing.T) {
			testCodecServerBrowserOrigin(t, codecHTTPServer, originCase.origin, originCase.isAllowed)
		})
	}
}

func testCodecServerBrowserOrigin(t *testing.T, codecHTTPServer *httptest.Server, origin string, isAllowed bool) {
	t.Helper()
	workflowInput := &dexpb.InterpreterWorkflowInput{FlowType: "local-codec-test"}
	binaryPayloadConverter := converter.NewProtoPayloadConverter()
	binaryPayload, err := binaryPayloadConverter.ToPayload(workflowInput)
	require.NoError(t, err)
	payloads := &commonpb.Payloads{Payloads: []*commonpb.Payload{binaryPayload}}

	for _, endpointPath := range []string{"/decode", "/encode"} {
		preflightRequest, err := http.NewRequest(http.MethodOptions, codecHTTPServer.URL+endpointPath, nil)
		require.NoError(t, err)
		preflightRequest.Header.Set("Origin", origin)
		preflightRequest.Header.Set(corsRequestedMethodHeader, http.MethodPost)
		preflightRequest.Header.Set("Access-Control-Request-Headers", "content-type,x-namespace")
		preflightResponse, err := codecHTTPServer.Client().Do(preflightRequest)
		require.NoError(t, err)
		require.NoError(t, preflightResponse.Body.Close())
		require.Equal(t, "Origin", preflightResponse.Header.Get("Vary"))
		if isAllowed {
			require.Equal(t, http.StatusNoContent, preflightResponse.StatusCode)
			require.Equal(t, origin, preflightResponse.Header.Get(corsAllowedOriginHeader))
			if origin != "" {
				require.Equal(t, corsAllowedMethods, preflightResponse.Header.Get(corsAllowedMethodsHeader))
				require.Equal(t, corsAllowedHeaders, preflightResponse.Header.Get(corsAllowedHeadersHeader))
			}
		} else {
			require.Equal(t, http.StatusForbidden, preflightResponse.StatusCode)
			require.Empty(t, preflightResponse.Header.Get(corsAllowedOriginHeader))
		}

		requestBody, err := protojson.Marshal(payloads)
		require.NoError(t, err)
		payloadRequest, err := http.NewRequest(http.MethodPost, codecHTTPServer.URL+endpointPath, bytes.NewReader(requestBody))
		require.NoError(t, err)
		payloadRequest.Header.Set("Origin", origin)
		payloadRequest.Header.Set("Content-Type", "application/json")
		payloadRequest.Header.Set("X-Namespace", "local-codec-test")
		payloadResponse, err := codecHTTPServer.Client().Do(payloadRequest)
		require.NoError(t, err)
		responseBody, err := io.ReadAll(payloadResponse.Body)
		require.NoError(t, payloadResponse.Body.Close())
		require.NoError(t, err)
		if !isAllowed {
			require.Equal(t, http.StatusForbidden, payloadResponse.StatusCode)
			require.Empty(t, payloadResponse.Header.Get(corsAllowedOriginHeader))
			continue
		}
		require.Equal(t, http.StatusOK, payloadResponse.StatusCode, string(responseBody))
		require.Equal(t, origin, payloadResponse.Header.Get(corsAllowedOriginHeader))
		require.NoError(t, protojson.Unmarshal(responseBody, payloads))
		require.Len(t, payloads.Payloads, 1)
		if endpointPath == "/decode" {
			require.Equal(t, converter.MetadataEncodingProtoJSON, string(payloads.Payloads[0].Metadata[converter.MetadataEncoding]))
		} else {
			roundTripInput := &dexpb.InterpreterWorkflowInput{}
			require.NoError(t, binaryPayloadConverter.FromPayload(payloads.Payloads[0], roundTripInput))
			require.True(t, proto.Equal(workflowInput, roundTripInput))
		}
	}
}
