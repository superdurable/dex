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

package aiagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"

	defaultOpenAIModel   = "gpt-5-mini"
	defaultOpenAIBaseURL = "https://api.openai.com/v1"
	maxErrorBodyBytes    = 2 << 10
	// Every ModelProviderError message starts with this prefix.
	modelProviderErrorPrefix = "model provider API"
)

const systemPrompt = `You are a helpful agent. Use fetch_page to read a web page, publish_note to
publish a short note, post_to_slack (when offered) to post to the team's Slack channel, wait to
pause for a number of seconds, and ask_user when you need information only the user has. The user
approves every note and Slack post. Keep replies short.`

// Message is one entry of the conversation the model replays on every call.
type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
	// ToolCallID, ToolName and Outcome describe a tool result.
	ToolCallID string    `json:"toolCallId,omitempty"`
	ToolName   string    `json:"toolName,omitempty"`
	Outcome    string    `json:"outcome,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// ToolCall is one call the model asked for. Arguments is the JSON object the model wrote.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ModelRequest is everything one model call needs; the Flow rebuilds it from Dex state each time.
type ModelRequest struct {
	SystemPrompt string
	Messages     []Message
	Tools        []ToolDefinition
}

// ModelClient returns the assistant's next message, which may ask for tool calls.
type ModelClient interface {
	Name() string
	Complete(ctx context.Context, request ModelRequest) (Message, error)
}

// ModelProviderError reports that the model provider's API failed or could not be reached.
type ModelProviderError struct {
	// StatusCode is zero when the API could not be reached.
	StatusCode int
	Detail     string
}

func (err *ModelProviderError) Error() string {
	if err.StatusCode == 0 {
		return modelProviderErrorPrefix + " unreachable: " + err.Detail
	}
	return fmt.Sprintf("%s returned HTTP %d: %s", modelProviderErrorPrefix, err.StatusCode, err.Detail)
}

// NewModelClientFromEnvironment uses OpenAI when OPENAI_API_KEY is set and the mock model otherwise.
func NewModelClientFromEnvironment() ModelClient {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return MockModel{}
	}
	return &OpenAIModel{
		BaseURL:    environmentOr("OPENAI_BASE_URL", defaultOpenAIBaseURL),
		APIKey:     apiKey,
		Model:      environmentOr("DEX_AI_AGENT_MODEL", defaultOpenAIModel),
		HTTPClient: &http.Client{Timeout: 90 * time.Second},
	}
}

// OpenAIModel calls an OpenAI-compatible Chat Completions API with function tools.
type OpenAIModel struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

func (model *OpenAIModel) Name() string { return model.Model }

func (model *OpenAIModel) Complete(ctx context.Context, request ModelRequest) (Message, error) {
	body, err := json.Marshal(chatCompletionRequest{
		Model:    model.Model,
		Messages: chatMessages(request),
		Tools:    chatTools(request.Tools),
	})
	if err != nil {
		return Message{}, fmt.Errorf("encode the model request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(model.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, fmt.Errorf("create the model request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+model.APIKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := model.HTTPClient.Do(httpRequest)
	if err != nil {
		if ctx.Err() != nil {
			// The Step attempt was canceled or timed out: that is Dex's failure, not the provider's.
			return Message{}, ctx.Err()
		}
		return Message{}, &ModelProviderError{Detail: err.Error()}
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusMultipleChoices {
		return Message{}, &ModelProviderError{StatusCode: response.StatusCode, Detail: providerErrorMessage(response.Body)}
	}
	var completion chatCompletionResponse
	if err := json.NewDecoder(response.Body).Decode(&completion); err != nil {
		return Message{}, &ModelProviderError{StatusCode: response.StatusCode, Detail: "undecodable response: " + err.Error()}
	}
	if len(completion.Choices) == 0 {
		return Message{}, &ModelProviderError{StatusCode: response.StatusCode, Detail: "the response has no choices"}
	}
	reply := completion.Choices[0].Message
	message := Message{Role: RoleAssistant, Content: reply.Content}
	for _, call := range reply.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
	}
	return message, nil
}

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []chatTool    `json:"tools,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatTool struct {
	Type     string         `json:"type"`
	Function ToolDefinition `json:"function"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

func chatMessages(request ModelRequest) []chatMessage {
	messages := []chatMessage{{Role: "system", Content: request.SystemPrompt}}
	for _, message := range request.Messages {
		converted := chatMessage{Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID}
		for _, call := range message.ToolCalls {
			chatCall := chatToolCall{ID: call.ID, Type: "function"}
			chatCall.Function.Name, chatCall.Function.Arguments = call.Name, call.Arguments
			converted.ToolCalls = append(converted.ToolCalls, chatCall)
		}
		messages = append(messages, converted)
	}
	return messages
}

func chatTools(definitions []ToolDefinition) []chatTool {
	tools := make([]chatTool, 0, len(definitions))
	for _, definition := range definitions {
		tools = append(tools, chatTool{Type: "function", Function: definition})
	}
	return tools
}

func providerErrorMessage(body io.Reader) string {
	text, err := io.ReadAll(io.LimitReader(body, maxErrorBodyBytes))
	if err != nil {
		return "unreadable error body: " + err.Error()
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(text, &payload) == nil && payload.Error.Message != "" {
		return payload.Error.Message
	}
	return strings.TrimSpace(string(text))
}

// MockModel turns slash commands into tool calls, so the example runs without a model key.
type MockModel struct{}

const mockHelp = "I am the mock model. Try /fetch <url>, /publish <text>, /slack <text>, /wait <seconds> or /ask <question>. " +
	"Set OPENAI_API_KEY to use a real model."

func (MockModel) Name() string { return "mock" }

func (MockModel) Complete(_ context.Context, request ModelRequest) (Message, error) {
	if len(request.Messages) == 0 {
		return Message{Role: RoleAssistant, Content: mockHelp}, nil
	}
	last := request.Messages[len(request.Messages)-1]
	if last.Role == RoleTool {
		return Message{Role: RoleAssistant, Content: mockToolSummary(last)}, nil
	}
	command, argument, _ := strings.Cut(strings.TrimSpace(last.Content), " ")
	argument = strings.TrimSpace(argument)
	call := ToolCall{ID: "mock-call-" + strconv.Itoa(len(request.Messages))}
	switch {
	case command == "/fetch" && argument != "":
		call.Name, call.Arguments = ToolFetchPage, mockArguments(map[string]any{"url": argument})
	case command == "/publish" && argument != "":
		call.Name, call.Arguments = ToolPublishNote, mockArguments(map[string]any{"text": argument})
	case command == "/slack" && argument != "":
		call.Name, call.Arguments = ToolPostToSlack, mockArguments(map[string]any{"text": argument})
	case command == "/ask" && argument != "":
		call.Name, call.Arguments = ToolAskUser, mockArguments(map[string]any{"question": argument})
	case command == "/wait":
		seconds, err := strconv.Atoi(argument)
		if err != nil || seconds <= 0 {
			return Message{Role: RoleAssistant, Content: "Use /wait <seconds> with a positive number."}, nil
		}
		call.Name, call.Arguments = ToolWait, mockArguments(map[string]any{"seconds": seconds})
	default:
		return Message{Role: RoleAssistant, Content: mockHelp}, nil
	}
	return Message{Role: RoleAssistant, ToolCalls: []ToolCall{call}}, nil
}

func mockToolSummary(result Message) string {
	switch result.Outcome {
	case OutcomeDone:
		return fmt.Sprintf("Done (%s): %s", result.ToolName, firstLine(result.Content))
	case OutcomeDeclined:
		return fmt.Sprintf("OK, I did not run %s.", result.ToolName)
	}
	return fmt.Sprintf("%s did not finish: %s", result.ToolName, firstLine(result.Content))
}

func mockArguments(arguments map[string]any) string {
	encoded, err := json.Marshal(arguments)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return truncate(line, 200)
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[:limit], "") + "…"
}

func environmentOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
