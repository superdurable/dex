// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package payloadcodec

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	remoteCodecRequestTimeout = 30 * time.Second
	maxRemoteResponseBytes    = 128 * 1024 * 1024
)

// RemoteCodec calls a codec server's /encode and /decode endpoints.
type RemoteCodec struct {
	endpoint string
	token    string
	client   *http.Client
}

// NewRemoteCodec returns a RemoteCodec for endpoint, a base URL such as http://127.0.0.1:8805.
// The endpoint must use https, or http on a loopback host. A non-empty token is sent as a bearer token.
func NewRemoteCodec(endpoint string, token string) (*RemoteCodec, error) {
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil || parsedEndpoint.Host == "" {
		return nil, fmt.Errorf("codec endpoint must be an absolute URL with a host")
	}
	switch parsedEndpoint.Scheme {
	case "https":
	case "http":
		if !isLoopbackHostName(parsedEndpoint.Hostname()) {
			return nil, fmt.Errorf("codec endpoint %s must use https; http is allowed only for localhost, 127.0.0.0/8, and ::1", parsedEndpoint.Redacted())
		}
	default:
		return nil, fmt.Errorf("codec endpoint %s must use https or http", parsedEndpoint.Redacted())
	}
	if parsedEndpoint.User != nil || parsedEndpoint.RawQuery != "" || parsedEndpoint.Fragment != "" {
		return nil, fmt.Errorf("codec endpoint must not contain credentials, a query, or a fragment")
	}
	return &RemoteCodec{
		endpoint: strings.TrimRight(parsedEndpoint.String(), "/"),
		token:    token,
		client: &http.Client{
			Timeout: remoteCodecRequestTimeout,
			// A redirect could send the token to another host.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// codecServerContext is the "context" object of the codec server protocol.
type codecServerContext struct {
	FlowType string `json:"flow_type"`
	FlowID   string `json:"flow_id"`
}

type encodeRequest struct {
	Context codecServerContext `json:"context"`
	Values  []json.RawMessage  `json:"values"`
}

type encodeResponse struct {
	Envelopes []string `json:"envelopes"`
}

type decodeRequest struct {
	Context   codecServerContext `json:"context"`
	Envelopes []string           `json:"envelopes"`
}

type decodeResponse struct {
	Values []json.RawMessage `json:"values"`
}

type errorResponse struct {
	Error struct {
		Category string `json:"category"`
		Message  string `json:"message"`
		Index    *int   `json:"index,omitempty"`
	} `json:"error"`
}

// EncodeValues posts the Values to /encode.
func (codec *RemoteCodec) EncodeValues(
	ctx context.Context,
	payloadContext dex.PayloadContext,
	values []*dexpb.Value,
) ([][]byte, error) {
	request := encodeRequest{Context: newCodecServerContext(payloadContext), Values: make([]json.RawMessage, 0, len(values))}
	for _, value := range values {
		encoded, err := protojson.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode value JSON: %w", err)
		}
		request.Values = append(request.Values, encoded)
	}
	var response encodeResponse
	if err := codec.post(ctx, "/encode", request, &response); err != nil {
		return nil, err
	}
	if len(response.Envelopes) != len(values) {
		return nil, fmt.Errorf("codec server returned %d envelopes for %d values", len(response.Envelopes), len(values))
	}
	envelopes := make([][]byte, 0, len(response.Envelopes))
	for index, encoded := range response.Envelopes {
		envelope, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil {
			return nil, newEntryError(index, CategoryMalformed, "codec server returned an envelope that is not base64")
		}
		envelopes = append(envelopes, envelope)
	}
	return envelopes, nil
}

// DecodeEnvelopes posts the envelopes to /decode and checks that each Value is of an encryptable kind.
func (codec *RemoteCodec) DecodeEnvelopes(
	ctx context.Context,
	payloadContext dex.PayloadContext,
	envelopes [][]byte,
) ([]*dexpb.Value, error) {
	request := decodeRequest{Context: newCodecServerContext(payloadContext), Envelopes: make([]string, 0, len(envelopes))}
	for _, envelope := range envelopes {
		request.Envelopes = append(request.Envelopes, base64.StdEncoding.EncodeToString(envelope))
	}
	var response decodeResponse
	if err := codec.post(ctx, "/decode", request, &response); err != nil {
		return nil, err
	}
	if len(response.Values) != len(envelopes) {
		return nil, fmt.Errorf("codec server returned %d values for %d envelopes", len(response.Values), len(envelopes))
	}
	values := make([]*dexpb.Value, 0, len(response.Values))
	for index, encoded := range response.Values {
		value := &dexpb.Value{}
		if err := protojson.Unmarshal(encoded, value); err != nil {
			return nil, newEntryError(index, CategoryInvalidPlaintext, "codec server returned a value that is not Value JSON")
		}
		if err := CheckEncryptableValue(value); err != nil {
			return nil, newEntryError(index, CategoryInvalidPlaintext, "codec server returned %v", err)
		}
		values = append(values, value)
	}
	return values, nil
}

func (codec *RemoteCodec) post(ctx context.Context, path string, requestBody any, responseBody any) error {
	body, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("encode codec server request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, codec.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create codec server request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if codec.token != "" {
		request.Header.Set("Authorization", "Bearer "+codec.token)
	}
	response, err := codec.client.Do(request)
	if err != nil {
		return fmt.Errorf("call codec server %s: %w", path, err)
	}
	responseBytes, readErr := io.ReadAll(io.LimitReader(response.Body, maxRemoteResponseBytes))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return fmt.Errorf("read codec server response: %w", errors.Join(readErr, closeErr))
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		var failure errorResponse
		if json.Unmarshal(responseBytes, &failure) != nil || failure.Error.Category == "" {
			return fmt.Errorf("codec server %s returned HTTP %d", path, response.StatusCode)
		}
		codecError := &CodecError{Category: failure.Error.Category, Message: failure.Error.Message, Index: -1}
		if failure.Error.Index != nil {
			codecError.Index = *failure.Error.Index
		}
		return codecError
	}
	if err := json.Unmarshal(responseBytes, responseBody); err != nil {
		return fmt.Errorf("codec server %s returned invalid JSON", path)
	}
	return nil
}

func newCodecServerContext(payloadContext dex.PayloadContext) codecServerContext {
	return codecServerContext{FlowType: payloadContext.FlowType, FlowID: payloadContext.FlowID}
}

func isLoopbackHostName(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
