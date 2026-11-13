// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

const (
	agentSessionStartStepType           = "StartAgentSession"
	agentSessionSendUserMessageRPC      = "send_user_message"
	agentSessionUpdateSettingsRPC       = "update_session_settings"
	maximumAgentSessionSettingsBytes    = 16 * 1024
	maximumAgentSessionSenderNameLength = 64
	agentSessionNotDeclaredErrorMessage = "flowType is not a Dex Agents session Flow type"
)

// agentSessionActionRPCNames are the Actions every Dex Agents session graph declares; they share one permission.
var agentSessionActionRPCNames = []string{"resolve_interrupt", "steer_user_message", "cancel_turn", "answer_question"}

type v2AgentSessionStartRequest struct {
	FlowType        string          `json:"flowType"`
	FlowID          string          `json:"flowId"`
	InitialMessage  *string         `json:"initialMessage"`
	SessionSettings json.RawMessage `json:"sessionSettings"`
}

type v2AgentSessionMessageRequest struct {
	FlowType             string   `json:"flowType"`
	FlowID               string   `json:"flowId"`
	WorkQueuePermissions []string `json:"workQueuePermissions"`
	Text                 string   `json:"text"`
	// SenderName is the display name the sender declared, a JSON string or null. It is not
	// authenticated. It stays raw because decoding turns a lone surrogate escape into U+FFFD.
	SenderName json.RawMessage `json:"senderName"`
}

type v2AgentSessionSettingsRequest struct {
	FlowType             string          `json:"flowType"`
	FlowID               string          `json:"flowId"`
	WorkQueuePermissions []string        `json:"workQueuePermissions"`
	SessionSettings      json.RawMessage `json:"sessionSettings"`
}

func registerAgentSessionHandlers(mux *http.ServeMux, handler *v2Handler) {
	mux.HandleFunc("POST /api/v2/agent-sessions/start", handler.startAgentSession)
	mux.HandleFunc("POST /api/v2/agent-sessions/messages", handler.sendAgentSessionMessage)
	mux.HandleFunc("POST /api/v2/agent-sessions/settings", handler.updateAgentSessionSettings)
}

// startAgentSession starts a session as AgentSessionClient.start_session does, including its search mode.
func (h *v2Handler) startAgentSession(response http.ResponseWriter, request *http.Request) {
	var body v2AgentSessionStartRequest
	if err := decodeJSON(response, request, &body); err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	snapshot, ok := h.loadSnapshot(response, request, []string{body.FlowType}, true)
	if !ok {
		return
	}
	definition, exists := snapshot.Definitions[body.FlowType]
	if !exists || definition.AgentSession == nil {
		WriteError(response, http.StatusBadRequest, agentSessionNotDeclaredErrorMessage, nil)
		return
	}
	if strings.TrimSpace(body.FlowID) == "" {
		WriteError(response, http.StatusBadRequest, "flow ID is required", nil)
		return
	}
	if err := validateFlowID(body.FlowID); err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	input := map[string]json.RawMessage{"initial_message": json.RawMessage("null"), "session_settings": json.RawMessage("null")}
	if body.InitialMessage != nil && strings.TrimSpace(*body.InitialMessage) != "" {
		message, err := json.Marshal(*body.InitialMessage)
		if err != nil {
			WriteError(response, http.StatusBadRequest, err.Error(), nil)
			return
		}
		input["initial_message"] = message
	}
	if len(body.SessionSettings) > 0 && !bytes.Equal(bytes.TrimSpace(body.SessionSettings), []byte("null")) {
		settings, err := compactAgentSessionSettings(body.SessionSettings)
		if err != nil {
			WriteError(response, http.StatusBadRequest, err.Error(), nil)
			return
		}
		input["session_settings"] = settings
	}
	payload, err := json.Marshal(input)
	if err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	result, err := h.client.StartFlow(request.Context(), &dexpb.StartFlowRequest{
		FlowId: body.FlowID, FlowType: body.FlowType, StartStepType: agentSessionStartStepType,
		StepInput: &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
			Encoding: "json", Payload: payload,
		}}},
		RequestId:   uuid.NewString(),
		StepOptions: &dexpb.StepOptions{SkipWaitFor: definition.AgentSession.StartStepSkipWaitFor},
		FlowStartOptions: &dexpb.FlowStartOptions{
			FlowConfigOverride: &dexpb.FlowConfig{
				WorkerTarget: &dexpb.WorkerTarget{IsPullMode: true},
				// The agent inbox finds sessions by the waiting Steps they are in.
				ActiveStepSearchMode: dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR.Enum(),
			},
		},
	})
	if err != nil {
		writeGRPCError(response, err, "StartFlow")
		return
	}
	writeJSON(response, http.StatusOK, v2StartResponse{FlowID: body.FlowID, RunID: result.GetRunId()})
}

func (h *v2Handler) sendAgentSessionMessage(response http.ResponseWriter, request *http.Request) {
	var body v2AgentSessionMessageRequest
	if err := decodeJSON(response, request, &body); err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		WriteError(response, http.StatusBadRequest, "text is required", nil)
		return
	}
	rpcInput := map[string]interface{}{"text": body.Text, "plan_mode": false}
	senderName, isSenderNameReadable := decodeAgentSessionSenderName(body.SenderName)
	senderName = strings.TrimSpace(senderName)
	if !isSenderNameReadable || (senderName != "" && !isValidAgentSessionSenderName(senderName)) {
		WriteError(response, http.StatusBadRequest, fmt.Sprintf(
			"senderName must be a string of at most %d characters with no control, format, "+
				"surrogate, or line break characters", maximumAgentSessionSenderNameLength), nil)
		return
	}
	if senderName != "" {
		rpcInput["sender_name"] = senderName
	}
	input, err := json.Marshal(rpcInput)
	if err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	h.invokeAgentSessionRPC(response, request, body.FlowType, body.FlowID, body.WorkQueuePermissions,
		agentSessionSendUserMessageRPC, input)
}

// decodeAgentSessionSenderName returns the name of a raw JSON string, or "" for an absent or
// null field. It returns false for another JSON type, invalid UTF-8, or an unpaired
// surrogate escape, which encoding/json would otherwise decode as U+FFFD.
func decodeAgentSessionSenderName(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", true
	}
	if !utf8.Valid(trimmed) || hasUnpairedSurrogateEscape(trimmed) {
		return "", false
	}
	var senderName string
	if err := json.Unmarshal(trimmed, &senderName); err != nil {
		return "", false
	}
	return senderName, true
}

// hasUnpairedSurrogateEscape reports whether a JSON string token holds a \uD800-\uDFFF escape
// that is not a high surrogate escape followed by a low one.
func hasUnpairedSurrogateEscape(token []byte) bool {
	isAfterHighSurrogate := false
	for index := 0; index < len(token); index++ {
		if token[index] != '\\' || index+1 >= len(token) {
			if isAfterHighSurrogate {
				return true
			}
			continue
		}
		if token[index+1] != 'u' || index+6 > len(token) {
			if isAfterHighSurrogate {
				return true
			}
			index++
			continue
		}
		codeUnit, err := strconv.ParseUint(string(token[index+2:index+6]), 16, 16)
		if err != nil {
			return true
		}
		isHigh := codeUnit >= 0xD800 && codeUnit <= 0xDBFF
		isLow := codeUnit >= 0xDC00 && codeUnit <= 0xDFFF
		if isAfterHighSurrogate != isLow {
			return true
		}
		isAfterHighSurrogate = isHigh
		index += 5
	}
	return isAfterHighSurrogate
}

// isValidAgentSessionSenderName applies the agents cores' sender name rule, so the route
// explains a refusal instead of returning the RPC's bare false.
func isValidAgentSessionSenderName(senderName string) bool {
	if !utf8.ValidString(senderName) {
		return false
	}
	runes := []rune(senderName)
	if len(runes) < 1 || len(runes) > maximumAgentSessionSenderNameLength {
		return false
	}
	for _, character := range runes {
		if unicode.In(character, unicode.Cc, unicode.Cf, unicode.Cs, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return !unicode.Is(unicode.Zs, runes[0]) && !unicode.Is(unicode.Zs, runes[len(runes)-1])
}

func (h *v2Handler) updateAgentSessionSettings(response http.ResponseWriter, request *http.Request) {
	var body v2AgentSessionSettingsRequest
	if err := decodeJSON(response, request, &body); err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	settings, err := compactAgentSessionSettings(body.SessionSettings)
	if err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	input, err := json.Marshal(map[string]json.RawMessage{"session_settings": settings})
	if err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	h.invokeAgentSessionRPC(response, request, body.FlowType, body.FlowID, body.WorkQueuePermissions,
		agentSessionUpdateSettingsRPC, input)
}

// invokeAgentSessionRPC applies the checks of invokeAction, using the session Actions' permission.
func (h *v2Handler) invokeAgentSessionRPC(
	response http.ResponseWriter,
	request *http.Request,
	flowType string,
	flowID string,
	workQueuePermissions []string,
	rpcName string,
	input []byte,
) {
	snapshot, ok := h.loadSnapshot(response, request, []string{flowType}, true)
	if !ok {
		return
	}
	permissions, permitted := h.resolvePermissions(response, workQueuePermissions)
	if !permitted {
		return
	}
	definition, exists := snapshot.Definitions[flowType]
	if !exists || flowID == "" {
		WriteError(response, http.StatusBadRequest, "flowType and flowId are required", nil)
		return
	}
	if definition.AgentSession == nil {
		WriteError(response, http.StatusBadRequest, agentSessionNotDeclaredErrorMessage, nil)
		return
	}
	if !definition.AgentSession.RPCNames[rpcName] {
		WriteError(response, http.StatusBadRequest, fmt.Sprintf("the session graph declares no RPC %s", rpcName), nil)
		return
	}
	requiredPermission := agentSessionActionPermission(definition)
	if requiredPermission == "" {
		WriteError(response, http.StatusBadRequest, "the session graph declares no session Action, so no permission guards it", nil)
		return
	}
	if !containsPermission(permissions, requiredPermission) {
		WriteError(response, http.StatusForbidden, "Action permission denied", nil)
		return
	}
	if err := h.requireActiveFlow(request.Context(), flowID, flowType); err != nil {
		writeGRPCError(response, err, "GetFlowSummary")
		return
	}
	invokeRequest, err := newV2InvokeRPCRequest(definition, flowID, rpcName, &dexpb.Value{
		Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "json", Payload: input}},
	})
	if err != nil {
		WriteCodedError(response, http.StatusUnprocessableEntity, v2RPCLocksUnresolvedCode, err.Error())
		return
	}
	result, err := h.client.InvokeRPC(request.Context(), invokeRequest)
	if err != nil {
		writeV2ActionError(response, err, rpcName)
		return
	}
	output, err := h.hydrateV2Value(request.Context(), flowID, result.GetOutput())
	if err != nil {
		// The RPC already ran, so answer success with the blob reference the caller can load later.
		output = result.GetOutput()
	}
	writeJSON(response, http.StatusOK, v2ResponseSnapshot(map[string]interface{}{
		"invoked": true, "output": v2DexValue(output),
	}))
}

// agentSessionActionPermission returns the permission the session Actions share, or "" when none is declared.
func agentSessionActionPermission(definition V2Definition) string {
	for _, rpcName := range agentSessionActionRPCNames {
		if action, ok := findV2Action(definition.Actions, rpcName); ok {
			return action.RequiredPermission
		}
	}
	return ""
}

// compactAgentSessionSettings checks the settings are one JSON object within the limit the agents Clients apply.
func compactAgentSessionSettings(raw json.RawMessage) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, fmt.Errorf("sessionSettings must be a JSON object")
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, raw); err != nil {
		return nil, fmt.Errorf("sessionSettings must be a JSON object: %w", err)
	}
	if compacted.Len() > maximumAgentSessionSettingsBytes {
		return nil, fmt.Errorf(
			"sessionSettings is %d bytes of compact JSON, more than the %d byte limit",
			compacted.Len(), maximumAgentSessionSettingsBytes,
		)
	}
	return compacted.Bytes(), nil
}
