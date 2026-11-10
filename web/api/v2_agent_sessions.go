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
	"strings"

	"github.com/google/uuid"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

const (
	agentSessionStartStepType           = "StartAgentSession"
	agentSessionSendUserMessageRPC      = "send_user_message"
	agentSessionUpdateSettingsRPC       = "update_session_settings"
	maximumAgentSessionSettingsBytes    = 16 * 1024
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
	snapshot, ok := h.loadSnapshot(response, request, true)
	if !ok {
		return
	}
	definition, exists := snapshot.Definitions[body.FlowType]
	if !exists || agentSessionActionPermission(definition) == "" {
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
		StepOptions: &dexpb.StepOptions{SkipWaitFor: true},
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
	input, err := json.Marshal(map[string]interface{}{"text": body.Text, "plan_mode": false})
	if err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	h.invokeAgentSessionRPC(response, request, body.FlowType, body.FlowID, body.WorkQueuePermissions,
		agentSessionSendUserMessageRPC, input)
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
	snapshot, ok := h.loadSnapshot(response, request, true)
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
	requiredPermission := agentSessionActionPermission(definition)
	if requiredPermission == "" {
		WriteError(response, http.StatusBadRequest, agentSessionNotDeclaredErrorMessage, nil)
		return
	}
	if snapshot.Revision != "" && !containsPermission(permissions, requiredPermission) {
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

// agentSessionActionPermission returns the session Actions' permission, or "" for a Flow type that is not a session.
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
