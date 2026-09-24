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

package registry

import (
	"io"
	"net/http"
	"strings"

	openai "github.com/superdurable/dex-connectors-library/connectors/openai"
	connector "github.com/superdurable/dex-connectors-library/sdk/go"
)

const demoOpenAIResponse = `{
  "id":"resp_customer_refund_demo",
  "model":"gpt-5-mini",
  "status":"completed",
  "output":[{"content":[{"type":"output_text","text":"We have completed our review and prepared an update about your refund request."}]}],
  "usage":{"input_tokens":24,"output_tokens":14,"total_tokens":38}
}`

func newDemoOpenAIConnection() openai.Connection {
	reference := connector.ConnectionRef{Provider: openai.ConnectorID, Name: "customer-refund-demo"}
	client, err := openai.New(
		openai.Config{Endpoint: "http://127.0.0.1/v1"},
		connector.StaticCredentialProvider[openai.Credentials]{
			reference: {APIKey: connector.NewSecretString("demo-key")},
		},
		openai.WithHTTPClient(&http.Client{Transport: demoOpenAITransport{}}),
	)
	if err != nil {
		panic(err)
	}
	connection, err := openai.NewConnection(client, reference)
	if err != nil {
		panic(err)
	}
	return connection
}

type demoOpenAITransport struct{}

func (demoOpenAITransport) RoundTrip(request *http.Request) (*http.Response, error) {
	status := http.StatusOK
	body := demoOpenAIResponse
	if request.Method != http.MethodPost || request.URL.Path != "/v1/responses" {
		status = http.StatusNotFound
		body = `{"error":{"message":"demo endpoint not found"}}`
	}
	return &http.Response{
		StatusCode:    status,
		Header:        http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"req_customer_refund_demo"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       request,
	}, nil
}
