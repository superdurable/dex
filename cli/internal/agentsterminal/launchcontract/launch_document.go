// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package launchcontract implements the dexcli side of the agents terminal UI
// contract: launch checks, channel messages, and the contract's shared rules.
//
// agents/cli/TERMINAL_UI_CONTRACT.md is the specification.
package launchcontract

import (
	"encoding/json"
	"slices"
)

// The modes a launch document may name.
const (
	// ModeChat shows one session's chat.
	ModeChat = "chat"
	// ModeSessions picks a session to resume.
	ModeSessions = "sessions"
	// ModeConnectors lists the connector tools.
	ModeConnectors = "connectors"
)

// The local request methods of contract 1.
const (
	// MethodPutImage stores an image file for the next message.
	MethodPutImage = "put_image"
	// MethodStartSession starts a new session of the launch Flow type.
	MethodStartSession = "start_session"
	// MethodListConnectors lists the connector tools.
	MethodListConnectors = "list_connectors"
	// MethodOpenURL opens a Dex Web page in the browser.
	MethodOpenURL = "open_url"
	// MethodGetRequestResult reads the remembered outcome of a key.
	MethodGetRequestResult = "get_request_result"
)

// The auth modes of dex.auth.
const (
	// AuthModeNone sends no credential.
	AuthModeNone = "none"
	// AuthModeBearer sends a bearer token from DEX_CREDENTIAL or DEX_CREDENTIAL_FILE.
	AuthModeBearer = "bearer"
	// AuthModeAPIKey exchanges DEX_API_KEY for access tokens.
	AuthModeAPIKey = "api_key"
)

// LaunchDocument is a launch line that passed every launch check.
type LaunchDocument struct {
	// Contract is the contract version, 1.
	Contract int `json:"contract"`
	// Mode is ModeChat, ModeSessions, or ModeConnectors.
	Mode string `json:"mode"`
	// Command names the agents command that launched dexcli.
	Command LaunchCommand `json:"command"`
	// Dex says how to reach the Dex server.
	Dex DexConnection `json:"dex"`
	// Session is the session to show, or in sessions mode only its Flow type.
	Session LaunchSession `json:"session"`
	// Display is text to show and never log.
	Display LaunchDisplay `json:"display"`
	// Methods are the local requests the launcher side serves.
	Methods []string `json:"methods"`
	// Capabilities come from the session Flow the Worker host built.
	Capabilities LaunchCapabilities `json:"capabilities"`
	// Notices are shown once the first frame is up.
	Notices []LaunchNotice `json:"notices"`
	// LogPath is the command's log file, for display.
	LogPath string `json:"log_path"`
	// Connectors holds the connectors mode rows as JSON, or nil.
	Connectors json.RawMessage `json:"connectors"`
}

// LaunchCommand names the agents command.
type LaunchCommand struct {
	// Name is the executable, such as "dex-agents-openai-sdk".
	Name string `json:"name"`
	// Harness starts every session ID of the command, such as "openai-sdk".
	Harness string `json:"harness"`
	// Language is "python" or "typescript".
	Language string `json:"language"`
	// Version is the command package version.
	Version string `json:"version"`
}

// DexConnection says how to reach the Dex server.
type DexConnection struct {
	// ServerAddress is the FlowService host:port.
	ServerAddress string `json:"server_address"`
	// TLS configures transport security.
	TLS DexTLS `json:"tls"`
	// Auth names how to authenticate; the secret is in the environment.
	Auth DexAuth `json:"auth"`
	// WebURL is the Dex Web base URL, or nil when none is known.
	WebURL *string `json:"web_url"`
}

// DexTLS configures transport security to the Dex server.
type DexTLS struct {
	// IsEnabled turns TLS on.
	IsEnabled bool `json:"enabled"`
	// CAFile is a PEM CA bundle path, or nil for the system roots.
	CAFile *string `json:"ca_file"`
	// ServerName is the name to verify, or nil for the address host.
	ServerName *string `json:"server_name"`
}

// DexAuth names how to authenticate to the Dex server.
type DexAuth struct {
	// Mode is AuthModeNone, AuthModeBearer, or AuthModeAPIKey.
	Mode string `json:"mode"`
	// Source is "env" or "file" for AuthModeBearer.
	Source string `json:"source"`
	// TokenURL is the token endpoint for AuthModeAPIKey.
	TokenURL string `json:"token_url"`
	// ServerID scopes AuthModeAPIKey access tokens.
	ServerID string `json:"server_id"`
}

// LaunchSession identifies the session.
type LaunchSession struct {
	// FlowType is the session Flow type.
	FlowType string `json:"flow_type"`
	// SessionID is the chat session's Flow ID.
	SessionID string `json:"session_id"`
	// IsResumed asks the chat to load the history.
	IsResumed bool `json:"is_resumed"`
}

// LaunchDisplay is text to show and never log.
type LaunchDisplay struct {
	// Model is the configured model, or nil.
	Model *string `json:"model"`
	// Workspace is the workspace path with the home directory as ~.
	Workspace string `json:"workspace"`
	// ConfigPath is the configuration path, or nil.
	ConfigPath *string `json:"config_path"`
	// Models are the model IDs /model offers, the configured model first;
	// nil when the launcher sends none.
	Models []string `json:"models"`
}

// LaunchCapabilities says what the built session Flow supports.
type LaunchCapabilities struct {
	// Images holds the image limits, or nil when the runner takes no images.
	Images *ImageLimits `json:"images"`
	// IsPlanMode reports a Flow built with plan mode.
	IsPlanMode bool `json:"plan_mode"`
	// HasTurnControl reports a Flow that supports steer and cancel.
	HasTurnControl bool `json:"turn_control"`
	// ReasoningEffort holds the reasoning effort values, or nil when the
	// command has no reasoning effort setting.
	ReasoningEffort *ReasoningEffortValues `json:"reasoning_effort"`
	// HasLauncherSessionSwitch makes a session switch end dexcli with a
	// switch_session message, so the launcher opens that session.
	HasLauncherSessionSwitch bool `json:"launcher_session_switch"`
}

// ReasoningEffortValues lists the reasoning_effort session setting values a
// command accepts.
type ReasoningEffortValues struct {
	// Values are the accepted values, from least to most effort.
	Values []string `json:"values"`
}

// ImageLimits bounds the images of one user message.
type ImageLimits struct {
	// MaxPerMessage is the most images one message carries.
	MaxPerMessage int64 `json:"max_per_message"`
	// MaxBytes is the largest image size.
	MaxBytes int64 `json:"max_bytes"`
}

// LaunchNotice is one notice of the launch document.
type LaunchNotice struct {
	// Level is "info", "warning", or "error".
	Level string `json:"level"`
	// Text is the notice text.
	Text string `json:"text"`
}

// HasMethod reports whether the launcher side serves method.
func (document LaunchDocument) HasMethod(method string) bool {
	return slices.Contains(document.Methods, method)
}

// WebURL returns the Dex Web base URL, or "" when none is known.
func (document LaunchDocument) WebURL() string {
	if document.Dex.WebURL == nil {
		return ""
	}
	return *document.Dex.WebURL
}
