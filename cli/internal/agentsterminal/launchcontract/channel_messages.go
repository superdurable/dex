// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package launchcontract

import (
	"encoding/json"
	"fmt"
	"slices"
	"unicode/utf8"
)

// The message types of contract 1.
const (
	MessageTypeLaunch         = "launch"
	MessageTypeResponse       = "response"
	MessageTypeNotice         = "notice"
	MessageTypeWorker         = "worker"
	MessageTypeReady          = "ready"
	MessageTypeLaunchRejected = "launch_rejected"
	MessageTypeRequest        = "request"
	MessageTypeCancel         = "cancel"
	MessageTypeResult         = "result"
)

var (
	launcherMessageTypes    = []string{MessageTypeResponse, MessageTypeNotice, MessageTypeWorker}
	terminalUIMessageTypes  = []string{MessageTypeReady, MessageTypeLaunchRejected, MessageTypeRequest, MessageTypeCancel, MessageTypeResult}
	contractOneMessageTypes = append(append([]string{MessageTypeLaunch}, launcherMessageTypes...), terminalUIMessageTypes...)
)

// The Worker states of a worker message.
const (
	WorkerStateReady        = "ready"
	WorkerStateReconnecting = "reconnecting"
	WorkerStateStopping     = "stopping"
	WorkerStateStopped      = "stopped"
)

// The common error codes of every method.
const (
	ErrorCodeUnknownMethod = "unknown_method"
	ErrorCodeInvalidParams = "invalid_params"
	ErrorCodeTimeout       = "timeout"
	ErrorCodeBusy          = "busy"
	ErrorCodeCancelled     = "cancelled"
	ErrorCodeHostStopped   = "host_stopped"
)

// ChannelError is the error of a response.
type ChannelError struct {
	// Code is a lowercase snake_case code.
	Code string `json:"code"`
	// Message is one English sentence without secrets.
	Message string `json:"message"`
}

func (channelError *ChannelError) Error() string {
	return channelError.Code + ": " + channelError.Message
}

// LauncherMessage is one valid message from the launcher after the launch
// line. Exactly one field is set, or none for a later message type to skip.
type LauncherMessage struct {
	// Response answers a request.
	Response *ResponseMessage
	// Notice is a user-facing notice.
	Notice *NoticeMessage
	// Worker reports the Worker host's state.
	Worker *WorkerMessage
}

// ResponseMessage answers one request.
type ResponseMessage struct {
	// ID is the request's ID.
	ID int64
	// Result is the result object, or nil when Error is set.
	Result json.RawMessage
	// Error is the error, or nil when Result is set.
	Error *ChannelError
}

// NoticeMessage is a notice from the Worker host.
type NoticeMessage struct {
	// Level is "info", "warning", or "error".
	Level string `json:"level"`
	// Text is the notice text.
	Text string `json:"text"`
}

// WorkerMessage reports the Worker host's state.
type WorkerMessage struct {
	// State is one of the WorkerState values.
	State string `json:"state"`
	// ExitCode is the exit status with WorkerStateStopped.
	ExitCode int `json:"exit_code"`
}

// DecodeChannelLine decodes one channel line into an object with a string type.
//
// It returns an error for a protocol error: too long, not UTF-8 JSON, not an
// object, or without a string type.
func DecodeChannelLine(line []byte) (map[string]any, error) {
	if len(line) > MaxLineBytes {
		return nil, fmt.Errorf("channel line is longer than 1 MiB")
	}
	if !utf8.Valid(line) {
		return nil, fmt.Errorf("channel line is not UTF-8")
	}
	value, err := decodeJSONValue(line)
	if err != nil {
		return nil, fmt.Errorf("channel line is not JSON: %w", err)
	}
	object, isObject := value.(map[string]any)
	if !isObject {
		return nil, fmt.Errorf("channel line is not a JSON object")
	}
	if _, isString := object["type"].(string); !isString {
		return nil, fmt.Errorf("channel message has no string type")
	}
	return object, nil
}

// ParseLauncherMessage checks a decoded message against the channel schema's
// from_launcher definitions and returns it typed.
//
// A known type from the wrong side and a malformed known type are errors. A
// later message type returns an empty LauncherMessage to skip.
func ParseLauncherMessage(object map[string]any) (LauncherMessage, error) {
	if violation := CheckLauncherMessage(object); violation != nil {
		return LauncherMessage{}, violation
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return LauncherMessage{}, fmt.Errorf("encode channel message: %w", err)
	}
	switch object["type"] {
	case MessageTypeResponse:
		var wire struct {
			ID     int64           `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *ChannelError   `json:"error"`
		}
		if err := json.Unmarshal(encoded, &wire); err != nil {
			return LauncherMessage{}, fmt.Errorf("decode response: %w", err)
		}
		return LauncherMessage{Response: &ResponseMessage{ID: wire.ID, Result: wire.Result, Error: wire.Error}}, nil
	case MessageTypeNotice:
		var notice NoticeMessage
		if err := json.Unmarshal(encoded, &notice); err != nil {
			return LauncherMessage{}, fmt.Errorf("decode notice: %w", err)
		}
		return LauncherMessage{Notice: &notice}, nil
	case MessageTypeWorker:
		var worker WorkerMessage
		if err := json.Unmarshal(encoded, &worker); err != nil {
			return LauncherMessage{}, fmt.Errorf("decode worker message: %w", err)
		}
		return LauncherMessage{Worker: &worker}, nil
	}
	return LauncherMessage{}, nil
}

// CheckLauncherMessage checks a message against #/$defs/from_launcher.
func CheckLauncherMessage(object map[string]any) *SchemaViolation {
	return checkSideMessage(object, launcherMessageTypes)
}

// CheckTerminalUIMessage checks a message against #/$defs/from_tui.
func CheckTerminalUIMessage(object map[string]any) *SchemaViolation {
	return checkSideMessage(object, terminalUIMessageTypes)
}

func checkSideMessage(object map[string]any, sideTypes []string) *SchemaViolation {
	rules := &jsonRules{}
	messageType, isName := rules.pattern("/type", object["type"], lowercaseNamePattern)
	if !isName {
		return rules.violation
	}
	if !slices.Contains(contractOneMessageTypes, messageType) {
		return nil
	}
	if !slices.Contains(sideTypes, messageType) {
		rules.fail("/type", "oneOf")
		return rules.violation
	}
	switch messageType {
	case MessageTypeResponse:
		checkResponse(rules, object)
	case MessageTypeNotice:
		checkNotice(rules, "", object)
	case MessageTypeWorker:
		checkWorker(rules, object)
	case MessageTypeLaunchRejected:
		if rules.required("", object, "code", "message") {
			rules.pattern("/code", object["code"], lowercaseNamePattern)
			rules.string("/message", object["message"], 0)
		}
	case MessageTypeRequest:
		checkRequest(rules, object)
	case MessageTypeCancel:
		if rules.required("", object, "id") {
			rules.integer("/id", object["id"], 1, maximumRequestID)
		}
	case MessageTypeResult:
		if rules.required("", object, "resume_session_id") {
			rules.string("/resume_session_id", object["resume_session_id"], 1)
		}
	}
	return rules.violation
}

func checkResponse(rules *jsonRules, object map[string]any) {
	if !rules.required("", object, "id") {
		return
	}
	rules.integer("/id", object["id"], 1, maximumRequestID)
	result, hasResult := object["result"]
	errorValue, hasError := object["error"]
	if hasResult == hasError {
		rules.fail("", "oneOf")
		return
	}
	if hasResult {
		rules.object("/result", result)
		return
	}
	checkChannelError(rules, "/error", errorValue)
}

func checkChannelError(rules *jsonRules, path string, value any) {
	channelError, isObject := rules.object(path, value)
	if !isObject || !rules.required(path, channelError, "code", "message") {
		return
	}
	rules.pattern(path+"/code", channelError["code"], lowercaseNamePattern)
	rules.string(path+"/message", channelError["message"], 0)
}

func checkWorker(rules *jsonRules, object map[string]any) {
	if !rules.required("", object, "state") {
		return
	}
	state, isState := rules.enum("/state", object["state"], WorkerStateReady, WorkerStateReconnecting, WorkerStateStopping, WorkerStateStopped)
	if !isState {
		return
	}
	exitCode, hasExitCode := object["exit_code"]
	switch {
	case state == WorkerStateStopped && !hasExitCode:
		rules.fail("", "required")
	case state != WorkerStateStopped && hasExitCode:
		rules.fail("", "not")
	case hasExitCode:
		rules.integer("/exit_code", exitCode, -1<<62, 1<<62)
	}
}

func checkRequest(rules *jsonRules, object map[string]any) {
	if !rules.required("", object, "type", "id", "method", "params") {
		return
	}
	rules.integer("/id", object["id"], 1, maximumRequestID)
	method, isMethod := rules.pattern("/method", object["method"], lowercaseNamePattern)
	params, isObject := rules.object("/params", object["params"])
	if !isMethod || !isObject {
		return
	}
	key, hasKey := object["idempotency_key"]
	if method == MethodGetRequestResult {
		if hasKey {
			rules.fail("", "not")
			return
		}
		if rules.required("/params", params, "idempotency_key") {
			rules.pattern("/params/idempotency_key", params["idempotency_key"], idempotencyKeyPattern)
		}
		return
	}
	if !hasKey {
		rules.fail("", "required")
		return
	}
	rules.pattern("/idempotency_key", key, idempotencyKeyPattern)
	switch method {
	case MethodPutImage:
		if rules.required("/params", params, "path") {
			if path, isString := rules.string("/params/path", params["path"], 0); isString && (path == "" || path[0] != '/') {
				rules.fail("/params/path", "pattern")
			}
		}
	case MethodOpenURL:
		if rules.required("/params", params, "url") {
			rules.string("/params/url", params["url"], 1)
		}
	}
}

// CheckMethodResult checks a response result against #/$defs/<method>_result.
// A method that contract 1 does not define passes.
func CheckMethodResult(method string, result map[string]any) *SchemaViolation {
	rules := &jsonRules{}
	switch method {
	case MethodPutImage:
		if rules.required("", result, "image", "name") {
			checkImageReference(rules, "/image", result["image"])
			rules.string("/name", result["name"], 1)
		}
	case MethodStartSession:
		if rules.required("", result, "session_id") {
			rules.pattern("/session_id", result["session_id"], sessionIDPattern)
		}
	case MethodListConnectors:
		checkConnectorList(rules, "", result)
	case MethodGetRequestResult:
		checkRequestResultResult(rules, result)
	}
	return rules.violation
}

func checkImageReference(rules *jsonRules, path string, value any) {
	image, isObject := rules.object(path, value)
	if !isObject || !rules.required(path, image, "uri", "mime_type", "size_bytes", "sha256") {
		return
	}
	if imageType, exists := image["type"]; exists {
		rules.constant(path+"/type", imageType, "image")
	}
	if uri, isString := rules.string(path+"/uri", image["uri"], 1); isString && len([]rune(uri)) > 1024 {
		rules.fail(path+"/uri", "maxLength")
	}
	rules.enum(path+"/mime_type", image["mime_type"], "image/png", "image/jpeg", "image/gif", "image/webp")
	rules.integer(path+"/size_bytes", image["size_bytes"], 1, 3_750_000)
	rules.pattern(path+"/sha256", image["sha256"], sha256HexPattern)
}

func checkRequestResultResult(rules *jsonRules, result map[string]any) {
	if !rules.required("", result, "state") {
		return
	}
	state, isState := rules.enum("/state", result["state"], "done", "running", "unknown")
	if !isState {
		return
	}
	outcome, hasResult := result["result"]
	errorValue, hasError := result["error"]
	if state != "done" {
		if hasResult || hasError {
			rules.fail("", "not")
		}
		return
	}
	if hasResult == hasError {
		rules.fail("", "oneOf")
		return
	}
	if hasResult {
		rules.object("/result", outcome)
		return
	}
	checkChannelError(rules, "/error", errorValue)
}
