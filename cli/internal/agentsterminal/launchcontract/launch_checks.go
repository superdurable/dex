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
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxLineBytes is the largest channel line, without its newline.
const MaxLineBytes = 1 << 20

// The launch_rejected codes.
const (
	// RejectionInvalidLaunch is a launch line that is not a valid launch document.
	RejectionInvalidLaunch = "invalid_launch"
	// RejectionContractUnsupported is a contract this dexcli does not support.
	RejectionContractUnsupported = "contract_unsupported"
	// RejectionModeUnsupported is a mode this dexcli does not run.
	RejectionModeUnsupported = "mode_unsupported"
	// RejectionAuthMissing is a missing or empty auth variable.
	RejectionAuthMissing = "auth_missing"
	// RejectionTerminalFailed is a terminal that could not be set up.
	RejectionTerminalFailed = "terminal_failed"
	// RejectionDexSetupFailed is a Dex client that could not be set up after
	// every launch check passed: an unreadable credential or CA file, or an
	// invalid Flow type or address.
	RejectionDexSetupFailed = "dex_setup_failed"
)

// LaunchRejection is a failed launch check: the launch_rejected message and
// the exit code.
type LaunchRejection struct {
	// Code is the launch_rejected code.
	Code string
	// Message is one English sentence without secrets.
	Message string
	// ExitCode is dexcli's exit code for Code.
	ExitCode int
	// Violation is the schema rule that failed, for schema rejections.
	Violation *SchemaViolation
}

func (rejection *LaunchRejection) Error() string {
	return rejection.Code + ": " + rejection.Message
}

// SupportedModes maps each supported contract version to the modes dexcli
// runs for it.
type SupportedModes map[int][]string

// CheckLaunchLine runs the launch checks in the contract's order.
//
// line is the first channel line without its newline. lookupEnv reads the
// environment; only the auth variable dex.auth names is read. It returns the
// document, or the first failed check. The terminal check comes later, from
// the caller.
func CheckLaunchLine(line []byte, supported SupportedModes, lookupEnv func(string) (string, bool)) (LaunchDocument, *LaunchRejection) {
	if len(line) > MaxLineBytes {
		return LaunchDocument{}, invalidLaunch("The launch document is longer than 1 MiB.", nil)
	}
	if !utf8.Valid(line) {
		return LaunchDocument{}, invalidLaunch("The launch document is not UTF-8.", nil)
	}
	value, err := decodeJSONValue(line)
	if err != nil {
		return LaunchDocument{}, invalidLaunch("The launch document is not a JSON object.", nil)
	}
	object, isObject := value.(map[string]any)
	if !isObject {
		return LaunchDocument{}, invalidLaunch("The launch document is not a JSON object.", nil)
	}
	if messageType, _ := object["type"].(string); messageType != "launch" {
		return LaunchDocument{}, invalidLaunch("The first channel message is not a launch document.", nil)
	}
	contract, isInteger := jsonInteger(object["contract"])
	modes, isSupported := supported[int(contract)]
	if !isInteger || !isSupported {
		return LaunchDocument{}, &LaunchRejection{
			Code:     RejectionContractUnsupported,
			Message:  contractUnsupportedMessage(supported, object["contract"], isInteger),
			ExitCode: 3,
		}
	}
	mode, _ := object["mode"].(string)
	if !slices.Contains(modes, mode) {
		return LaunchDocument{}, &LaunchRejection{
			Code:     RejectionModeUnsupported,
			Message:  fmt.Sprintf("This dexcli does not run the launch document's mode for contract %d.", contract),
			ExitCode: 3,
		}
	}
	if violation := CheckLaunchSchema(object); violation != nil {
		return LaunchDocument{}, invalidLaunch(fmt.Sprintf("The launch document is invalid: %s.", violation.Error()), violation)
	}
	var document LaunchDocument
	if err := json.Unmarshal(line, &document); err != nil {
		return LaunchDocument{}, invalidLaunch("The launch document does not decode.", nil)
	}
	if missing := MissingAuthVariable(lookupEnv, document.Dex.Auth); missing != "" {
		return LaunchDocument{}, &LaunchRejection{
			Code:     RejectionAuthMissing,
			Message:  fmt.Sprintf("%s is not set; dex.auth mode %s needs it.", missing, document.Dex.Auth.Mode),
			ExitCode: 2,
		}
	}
	return document, nil
}

func contractUnsupportedMessage(supported SupportedModes, contract any, isInteger bool) string {
	versions := make([]string, 0, len(supported))
	for version := range supported {
		versions = append(versions, strconv.Itoa(version))
	}
	sort.Strings(versions)
	supportedText := "terminal UI contract " + strings.Join(versions, ", ")
	if !isInteger {
		return "This dexcli supports " + supportedText + "; the launch document names no integer contract."
	}
	return fmt.Sprintf("This dexcli supports %s, not %v.", supportedText, contract)
}

// DexSetupFailed returns the rejection for a Dex client that could not be
// set up; message is one sentence without secrets or values.
func DexSetupFailed(message string) *LaunchRejection {
	return &LaunchRejection{Code: RejectionDexSetupFailed, Message: message, ExitCode: 1}
}

// TerminalFailed returns the rejection for a terminal that could not be set up.
func TerminalFailed(reason string) *LaunchRejection {
	return &LaunchRejection{Code: RejectionTerminalFailed, Message: "The terminal could not be set up: " + reason + ".", ExitCode: 1}
}

func invalidLaunch(message string, violation *SchemaViolation) *LaunchRejection {
	return &LaunchRejection{Code: RejectionInvalidLaunch, Message: message, ExitCode: 2, Violation: violation}
}

// CheckLaunchSchema checks a decoded launch object against
// terminal-ui-launch.schema.json and returns the first violation, or nil.
func CheckLaunchSchema(object map[string]any) *SchemaViolation {
	rules := &jsonRules{}
	if !rules.required("", object, "type", "contract", "mode", "command", "dex", "methods", "capabilities", "notices", "log_path") {
		return rules.violation
	}
	rules.constant("/type", object["type"], "launch")
	if contract, isInteger := jsonInteger(object["contract"]); !isInteger || contract != 1 {
		rules.fail("/contract", "const")
	}
	mode, _ := rules.enum("/mode", object["mode"], ModeChat, ModeSessions, ModeConnectors)
	checkCommand(rules, object["command"])
	checkDex(rules, object["dex"])
	if session, exists := object["session"]; exists {
		rules.object("/session", session)
	}
	if display, exists := object["display"]; exists {
		checkDisplay(rules, display)
	}
	checkMethodNames(rules, "/methods", object["methods"])
	checkCapabilities(rules, object["capabilities"])
	checkNotices(rules, object["notices"])
	rules.string("/log_path", object["log_path"], 1)
	if connectors, exists := object["connectors"]; exists {
		checkConnectorList(rules, "/connectors", connectors)
	}
	switch mode {
	case ModeChat:
		if rules.required("", object, "session") {
			checkChatSession(rules, object["session"])
		}
	case ModeSessions:
		if rules.required("", object, "session") {
			if session, isObject := rules.object("/session", object["session"]); isObject && rules.required("/session", session, "flow_type") {
				rules.string("/session/flow_type", session["flow_type"], 1)
			}
		}
	case ModeConnectors:
		rules.required("", object, "connectors")
	}
	return rules.violation
}

func checkCommand(rules *jsonRules, value any) {
	command, isObject := rules.object("/command", value)
	if !isObject || !rules.required("/command", command, "name", "harness", "language", "version") {
		return
	}
	rules.string("/command/name", command["name"], 1)
	rules.pattern("/command/harness", command["harness"], harnessNamePattern)
	rules.enum("/command/language", command["language"], "python", "typescript")
	rules.string("/command/version", command["version"], 1)
}

func checkDex(rules *jsonRules, value any) {
	dex, isObject := rules.object("/dex", value)
	if !isObject || !rules.required("/dex", dex, "server_address", "tls", "auth", "web_url") {
		return
	}
	rules.string("/dex/server_address", dex["server_address"], 1)
	if tls, isObject := rules.object("/dex/tls", dex["tls"]); isObject && rules.required("/dex/tls", tls, "enabled", "ca_file", "server_name") {
		rules.boolean("/dex/tls/enabled", tls["enabled"])
		rules.nullableString("/dex/tls/ca_file", tls["ca_file"])
		rules.nullableString("/dex/tls/server_name", tls["server_name"])
	}
	checkAuth(rules, dex["auth"])
	rules.nullableString("/dex/web_url", dex["web_url"])
}

func checkAuth(rules *jsonRules, value any) {
	auth, isObject := rules.object("/dex/auth", value)
	if !isObject || !rules.required("/dex/auth", auth, "mode") {
		return
	}
	mode, _ := rules.enum("/dex/auth/mode", auth["mode"], AuthModeNone, AuthModeBearer, AuthModeAPIKey)
	rules.forbidden("/dex/auth", auth, "credential", "api_key", "token", "access_token")
	switch mode {
	case AuthModeBearer:
		if rules.required("/dex/auth", auth, "source") {
			rules.enum("/dex/auth/source", auth["source"], "env", "file")
		}
	case AuthModeAPIKey:
		if rules.required("/dex/auth", auth, "token_url", "server_id") {
			rules.string("/dex/auth/token_url", auth["token_url"], 1)
			rules.string("/dex/auth/server_id", auth["server_id"], 1)
		}
	}
}

func checkChatSession(rules *jsonRules, value any) {
	session, isObject := rules.object("/session", value)
	if !isObject || !rules.required("/session", session, "flow_type", "session_id", "is_resumed") {
		return
	}
	rules.string("/session/flow_type", session["flow_type"], 1)
	rules.string("/session/session_id", session["session_id"], 1)
	rules.boolean("/session/is_resumed", session["is_resumed"])
}

func checkDisplay(rules *jsonRules, value any) {
	display, isObject := rules.object("/display", value)
	if !isObject {
		return
	}
	if model, exists := display["model"]; exists {
		rules.nullableString("/display/model", model)
	}
	if workspace, exists := display["workspace"]; exists {
		rules.string("/display/workspace", workspace, 0)
	}
	if configPath, exists := display["config_path"]; exists {
		rules.nullableString("/display/config_path", configPath)
	}
}

func checkMethodNames(rules *jsonRules, path string, value any) {
	methods, isArray := rules.array(path, value)
	if !isArray {
		return
	}
	seen := map[string]bool{}
	for index, method := range methods {
		name, isName := rules.pattern(itemPath(path, index), method, lowercaseNamePattern)
		if !isName {
			return
		}
		if seen[name] {
			rules.fail(path, "uniqueItems")
			return
		}
		seen[name] = true
	}
}

func checkCapabilities(rules *jsonRules, value any) {
	capabilities, isObject := rules.object("/capabilities", value)
	if !isObject {
		return
	}
	if images, exists := capabilities["images"]; exists {
		if limits, isObject := rules.object("/capabilities/images", images); isObject &&
			rules.required("/capabilities/images", limits, "max_per_message", "max_bytes") {
			rules.integer("/capabilities/images/max_per_message", limits["max_per_message"], 1, 1<<62)
			rules.integer("/capabilities/images/max_bytes", limits["max_bytes"], 1, 1<<62)
		}
	}
	for _, name := range []string{"plan_mode", "turn_control"} {
		if flag, exists := capabilities[name]; exists {
			rules.boolean("/capabilities/"+name, flag)
		}
	}
}

func checkNotices(rules *jsonRules, value any) {
	notices, isArray := rules.array("/notices", value)
	if !isArray {
		return
	}
	for index, notice := range notices {
		checkNotice(rules, itemPath("/notices", index), notice)
	}
}

func checkNotice(rules *jsonRules, path string, value any) {
	notice, isObject := rules.object(path, value)
	if !isObject || !rules.required(path, notice, "level", "text") {
		return
	}
	rules.enum(path+"/level", notice["level"], "info", "warning", "error")
	rules.string(path+"/text", notice["text"], 0)
}

func checkConnectorList(rules *jsonRules, path string, value any) {
	list, isObject := rules.object(path, value)
	if !isObject || !rules.required(path, list, "mcp_url", "tools") {
		return
	}
	rules.string(path+"/mcp_url", list["mcp_url"], 1)
	tools, isArray := rules.array(path+"/tools", list["tools"])
	for index, tool := range tools {
		if !isArray {
			break
		}
		toolPath := itemPath(path+"/tools", index)
		row, isObject := rules.object(toolPath, tool)
		if !isObject || !rules.required(toolPath, row, "name", "connector", "connection", "operation", "is_enabled") {
			return
		}
		rules.string(toolPath+"/name", row["name"], 1)
		rules.nullableString(toolPath+"/connector", row["connector"])
		rules.nullableString(toolPath+"/connection", row["connection"])
		rules.nullableString(toolPath+"/operation", row["operation"])
		rules.boolean(toolPath+"/is_enabled", row["is_enabled"])
	}
	connections, exists := list["connections"]
	if !exists || connections == nil {
		return
	}
	rows, isArray := rules.array(path+"/connections", connections)
	for index, connection := range rows {
		if !isArray {
			break
		}
		connectionPath := itemPath(path+"/connections", index)
		row, isObject := rules.object(connectionPath, connection)
		if !isObject || !rules.required(connectionPath, row, "connector", "connection", "status") {
			return
		}
		rules.string(connectionPath+"/connector", row["connector"], 0)
		rules.string(connectionPath+"/connection", row["connection"], 0)
		rules.string(connectionPath+"/status", row["status"], 0)
	}
}
