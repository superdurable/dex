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

// Tests run against the Dex server at DEX_FLOW_SERVICE_ADDRESS.

// Package harnessconfig reads the agent harness examples' settings from environment variables.
package harnessconfig

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"

	"github.com/superdurable/dex/examples/go/agent-harnesses/demomodel"
)

// Config holds the settings that the quick start and the order support app share.
type Config struct {
	// FlowServiceAddress is the Dex server's gRPC address that the Worker and the Client connect to.
	FlowServiceAddress string
	// HTTPAddress is the address the order support routes bind to.
	HTTPAddress string
	// BlobCacheDir is the directory of the blob cache that the Worker and the Client share.
	BlobCacheDir string
	// EinoModel is an OpenAI model name for eino-ext, or a "mock/" name for the local demo model.
	EinoModel string
	// OrderDeskToolDelay makes each order desk tool call wait first, so a demo can stop the Worker mid-call.
	OrderDeskToolDelay time.Duration
	// SessionKey signs session tokens. A random key ends every token when the process restarts.
	SessionKey []byte
}

// ConfigFromEnvironment reads the settings, with defaults for a local dexcli dev.
//
// It returns an error when DEX_ORDER_DESK_TOOL_DELAY_SECONDS is not a number.
func ConfigFromEnvironment() (Config, error) {
	toolDelaySeconds, err := strconv.ParseFloat(environmentOr("DEX_ORDER_DESK_TOOL_DELAY_SECONDS", "0"), 64)
	if err != nil {
		return Config{}, fmt.Errorf("DEX_ORDER_DESK_TOOL_DELAY_SECONDS must be a number of seconds: %w", err)
	}
	sessionKey := []byte(os.Getenv("DEX_AGENT_SESSION_KEY"))
	if len(sessionKey) == 0 {
		sessionKey = make([]byte, 32)
		if _, err := rand.Read(sessionKey); err != nil {
			return Config{}, fmt.Errorf("create a session key: %w", err)
		}
	}
	return Config{
		FlowServiceAddress: environmentOr("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801"),
		HTTPAddress:        environmentOr("DEX_AGENT_HARNESS_HTTP_ADDRESS", "127.0.0.1:8082"),
		BlobCacheDir:       environmentOr("DEX_BLOB_CACHE_DIR", filepath.Join(os.TempDir(), "dex-go-agent-harness-examples-blob-cache")),
		EinoModel:          environmentOr("DEX_EINO_MODEL", demomodel.DemoModelName),
		OrderDeskToolDelay: time.Duration(toolDelaySeconds * float64(time.Second)),
		SessionKey:         sessionKey,
	}, nil
}

// NewEinoChatModel returns the local demo model for a "mock/" name, or else an eino-ext OpenAI chat model.
//
// The OpenAI model reads its key from OPENAI_API_KEY. It returns an error when
// the key is missing or the model cannot be created.
func NewEinoChatModel(ctx context.Context, modelName string) (model.ToolCallingChatModel, error) {
	if demomodel.IsDemoModel(modelName) {
		return demomodel.NewOrderSupportDemoModel(), nil
	}
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, errors.New("set OPENAI_API_KEY to use the OpenAI model " + modelName)
	}
	return openai.NewChatModel(ctx, &openai.ChatModelConfig{APIKey: apiKey, Model: modelName})
}

func environmentOr(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
