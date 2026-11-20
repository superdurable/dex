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

package payloadencryptioninterop_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// codecServerEndpoint is one running codec server, as the run script describes it.
type codecServerEndpoint struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	Token         string `json:"token"`
	AllowedOrigin string `json:"allowed_origin"`
}

// codecServerPayloadCodec is a PayloadCodec that encodes and decodes through a
// codec server's HTTP protocol, so an SDK Client exercises that server end to end.
type codecServerPayloadCodec struct {
	endpoint   codecServerEndpoint
	httpClient *http.Client
}

type codecServerContext struct {
	FlowType string `json:"flow_type"`
	FlowID   string `json:"flow_id"`
}

type codecServerEncodeRequest struct {
	Context codecServerContext `json:"context"`
	Values  []json.RawMessage  `json:"values"`
}

type codecServerEncodeResponse struct {
	Envelopes []string `json:"envelopes"`
}

type codecServerDecodeRequest struct {
	Context   codecServerContext `json:"context"`
	Envelopes []string           `json:"envelopes"`
}

type codecServerDecodeResponse struct {
	Values []json.RawMessage `json:"values"`
}

func (codec codecServerPayloadCodec) Encode(
	ctx context.Context,
	payloadContext dex.PayloadContext,
	plaintexts [][]byte,
) ([]*dexpb.PayloadEnvelope, error) {
	request := codecServerEncodeRequest{
		Context: codecServerContextOf(payloadContext),
		Values:  make([]json.RawMessage, 0, len(plaintexts)),
	}
	for _, plaintext := range plaintexts {
		var value dexpb.Value
		if err := proto.Unmarshal(plaintext, &value); err != nil {
			return nil, fmt.Errorf("parse plaintext Value: %w", err)
		}
		valueJSON, err := protojson.Marshal(&value)
		if err != nil {
			return nil, err
		}
		request.Values = append(request.Values, valueJSON)
	}
	var response codecServerEncodeResponse
	if err := codec.post(ctx, "/encode", request, &response); err != nil {
		return nil, err
	}
	if len(response.Envelopes) != len(plaintexts) {
		return nil, fmt.Errorf("%s /encode returned %d envelopes for %d values",
			codec.endpoint.Name, len(response.Envelopes), len(plaintexts))
	}
	envelopes := make([]*dexpb.PayloadEnvelope, 0, len(response.Envelopes))
	for _, encodedEnvelope := range response.Envelopes {
		envelopeBytes, err := base64.StdEncoding.DecodeString(encodedEnvelope)
		if err != nil {
			return nil, fmt.Errorf("%s /encode returned invalid base64: %w", codec.endpoint.Name, err)
		}
		envelope := &dexpb.PayloadEnvelope{}
		if err := proto.Unmarshal(envelopeBytes, envelope); err != nil {
			return nil, fmt.Errorf("%s /encode returned an invalid envelope: %w", codec.endpoint.Name, err)
		}
		envelopes = append(envelopes, envelope)
	}
	return envelopes, nil
}

func (codec codecServerPayloadCodec) Decode(
	ctx context.Context,
	payloadContext dex.PayloadContext,
	envelopes []*dexpb.PayloadEnvelope,
) ([][]byte, error) {
	request := codecServerDecodeRequest{
		Context:   codecServerContextOf(payloadContext),
		Envelopes: make([]string, 0, len(envelopes)),
	}
	for _, envelope := range envelopes {
		envelopeBytes, err := proto.Marshal(envelope)
		if err != nil {
			return nil, err
		}
		request.Envelopes = append(request.Envelopes, base64.StdEncoding.EncodeToString(envelopeBytes))
	}
	var response codecServerDecodeResponse
	if err := codec.post(ctx, "/decode", request, &response); err != nil {
		return nil, err
	}
	if len(response.Values) != len(envelopes) {
		return nil, fmt.Errorf("%s /decode returned %d values for %d envelopes",
			codec.endpoint.Name, len(response.Values), len(envelopes))
	}
	plaintexts := make([][]byte, 0, len(response.Values))
	for _, valueJSON := range response.Values {
		var value dexpb.Value
		if err := protojson.Unmarshal(valueJSON, &value); err != nil {
			return nil, fmt.Errorf("%s /decode returned invalid Value JSON: %w", codec.endpoint.Name, err)
		}
		plaintext, err := proto.Marshal(&value)
		if err != nil {
			return nil, err
		}
		plaintexts = append(plaintexts, plaintext)
	}
	return plaintexts, nil
}

func (codec codecServerPayloadCodec) post(ctx context.Context, path string, requestBody any, responseBody any) error {
	body, err := json.Marshal(requestBody)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, codec.endpoint.URL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+codec.endpoint.Token)
	response, err := codec.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s returned HTTP %d: %s", codec.endpoint.Name, path, response.StatusCode, responseBytes)
	}
	return json.Unmarshal(responseBytes, responseBody)
}

func codecServerContextOf(payloadContext dex.PayloadContext) codecServerContext {
	return codecServerContext{FlowType: payloadContext.FlowType, FlowID: payloadContext.FlowID}
}

var _ dex.PayloadCodec = codecServerPayloadCodec{}
