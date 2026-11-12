// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package launchcontract

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// conformanceDirectory holds the contract's hand-written cases.
var conformanceDirectory = filepath.Join("..", "..", "..", "..", "agents", "conformance", "terminal_ui")

// allContractModes is what launch.json assumes dexcli runs.
var allContractModes = SupportedModes{1: {ModeChat, ModeSessions, ModeConnectors}}

func readConformanceFile(t *testing.T, name string, target any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(conformanceDirectory, name))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, target))
}

func environmentLookup(environment map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, isSet := environment[name]
		return value, isSet
	}
}

// Every launch.json case gets the expected acceptance, code, exit code, and
// schema location.
func TestLaunchConformanceCases(t *testing.T) {
	var file struct {
		Cases []struct {
			Name        string            `json:"name"`
			Environment map[string]string `json:"terminal_ui_environment"`
			Launch      json.RawMessage   `json:"launch"`
			Line        *string           `json:"line"`
			PadToBytes  int               `json:"pad_to_bytes"`
			Expected    struct {
				IsAccepted bool    `json:"accepted"`
				ExitCode   int     `json:"exit_code"`
				Code       string  `json:"code"`
				Path       *string `json:"path"`
				Keyword    string  `json:"keyword"`
			} `json:"expected"`
		} `json:"cases"`
	}
	readConformanceFile(t, "launch.json", &file)
	require.NotEmpty(t, file.Cases)
	for _, testCase := range file.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			var line []byte
			if testCase.Line != nil {
				line = []byte(*testCase.Line)
			} else {
				var compact bytes.Buffer
				require.NoError(t, json.Compact(&compact, testCase.Launch))
				line = compact.Bytes()
			}
			if padding := testCase.PadToBytes - len(line); padding > 0 {
				line = append(line, bytes.Repeat([]byte{' '}, padding)...)
			}
			document, rejection := CheckLaunchLine(line, allContractModes, environmentLookup(testCase.Environment))
			if testCase.Expected.IsAccepted {
				require.Nil(t, rejection)
				require.NotEmpty(t, document.Mode)
				return
			}
			require.NotNil(t, rejection)
			require.Equal(t, testCase.Expected.Code, rejection.Code)
			require.Equal(t, testCase.Expected.ExitCode, rejection.ExitCode)
			require.NotContains(t, rejection.Message, "not-a-secret", "a rejection never holds an environment value")
			if testCase.Expected.Path != nil {
				require.NotNil(t, rejection.Violation)
				require.Equal(t, *testCase.Expected.Path, rejection.Violation.Path)
				require.Equal(t, testCase.Expected.Keyword, rejection.Violation.Keyword)
			}
		})
	}
}

// A dexcli whose modes lack the pickers rejects them with mode_unsupported.
func TestLaunchChecksRejectModesThisDexcliDoesNotRun(t *testing.T) {
	line := []byte(`{"type":"launch","contract":1,"mode":"sessions"}`)
	_, rejection := CheckLaunchLine(line, SupportedModes{1: {ModeChat}}, environmentLookup(nil))
	require.NotNil(t, rejection)
	require.Equal(t, RejectionModeUnsupported, rejection.Code)
	require.Equal(t, 3, rejection.ExitCode)
}

// describe.json's documents pass or fail the schema, its negotiations choose
// the expected contract, and this dexcli's own --describe passes.
func TestDescribeConformanceCases(t *testing.T) {
	var file struct {
		Cases []struct {
			Name     string          `json:"name"`
			Describe json.RawMessage `json:"describe"`
			IsValid  bool            `json:"is_valid"`
		} `json:"cases"`
		Negotiation []struct {
			Name              string          `json:"name"`
			LauncherContracts []int           `json:"launcher_contracts"`
			Describe          json.RawMessage `json:"describe"`
			Mode              string          `json:"mode"`
			Expected          struct {
				Contract    int    `json:"contract"`
				PlainReason string `json:"plain_reason"`
			} `json:"expected"`
		} `json:"negotiation"`
	}
	readConformanceFile(t, "describe.json", &file)
	for _, testCase := range file.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			_, err := CheckDescribeOutput(append(testCase.Describe, '\n'))
			if testCase.IsValid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	for _, testCase := range file.Negotiation {
		t.Run("negotiation/"+testCase.Name, func(t *testing.T) {
			document, err := CheckDescribeOutput(testCase.Describe)
			require.NoError(t, err)
			choice := ChooseContract(testCase.LauncherContracts, document, testCase.Mode)
			require.Equal(t, testCase.Expected.Contract, choice.Contract)
			require.Equal(t, testCase.Expected.PlainReason, choice.PlainReason)
		})
	}
	own, err := json.Marshal(NewDescribeDocument("1.46.0", SupportedModes{1: {ModeChat}}))
	require.NoError(t, err)
	document, err := CheckDescribeOutput(append(own, '\n'))
	require.NoError(t, err)
	require.Equal(t, 1, ChooseContract([]int{1}, document, ModeChat).Contract)
	require.Equal(t, "mode_missing", ChooseContract([]int{1}, document, ModeSessions).PlainReason)
}

// Every messages.json case is valid or invalid at the expected place, for
// the side that sends it.
func TestMessageConformanceCases(t *testing.T) {
	var file struct {
		Cases []struct {
			Name      string          `json:"name"`
			Direction string          `json:"direction"`
			Message   json.RawMessage `json:"message"`
			IsValid   bool            `json:"is_valid"`
		} `json:"cases"`
		ResultCases []struct {
			Name    string          `json:"name"`
			Method  string          `json:"method"`
			Result  json.RawMessage `json:"result"`
			IsValid bool            `json:"is_valid"`
		} `json:"result_cases"`
	}
	readConformanceFile(t, "messages.json", &file)
	for _, testCase := range file.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			value, err := decodeJSONValue(testCase.Message)
			require.NoError(t, err)
			object, isObject := value.(map[string]any)
			require.True(t, isObject)
			var violation *SchemaViolation
			if testCase.Direction == "launcher" {
				violation = CheckLauncherMessage(object)
				if testCase.IsValid {
					_, parseErr := ParseLauncherMessage(object)
					require.NoError(t, parseErr)
				}
			} else {
				violation = CheckTerminalUIMessage(object)
			}
			if testCase.IsValid {
				require.Nil(t, violation)
			} else {
				require.NotNil(t, violation)
			}
		})
	}
	for _, testCase := range file.ResultCases {
		t.Run("result/"+testCase.Name, func(t *testing.T) {
			value, err := decodeJSONValue(testCase.Result)
			require.NoError(t, err)
			violation := CheckMethodResult(testCase.Method, value.(map[string]any))
			if testCase.IsValid {
				require.Nil(t, violation)
			} else {
				require.NotNil(t, violation)
			}
		})
	}
}

// The session ID vectors derive as written, invalid keys are refused, and
// every outcome a correct launcher may return passes dexcli's check of a
// start_session result.
func TestSessionIDConformanceCases(t *testing.T) {
	var file struct {
		Vectors []struct {
			Harness       string `json:"harness"`
			Key           string `json:"idempotency_key"`
			SessionID     string `json:"session_id"`
			LongSessionID string `json:"long_session_id"`
		} `json:"vectors"`
		InvalidKeys []struct {
			Key string `json:"idempotency_key"`
		} `json:"invalid_keys"`
		Collisions []struct {
			Harness        string   `json:"harness"`
			Keys           []string `json:"idempotency_keys"`
			SessionID      string   `json:"session_id"`
			LongSessionIDs []string `json:"long_session_ids"`
		} `json:"collisions"`
		StartSessionCases []struct {
			Name     string `json:"name"`
			Harness  string `json:"harness"`
			Key      string `json:"idempotency_key"`
			Expected struct {
				SessionID *string `json:"session_id"`
			} `json:"expected"`
		} `json:"start_session_cases"`
	}
	readConformanceFile(t, "session_ids.json", &file)
	require.NotEmpty(t, file.Vectors)
	for _, vector := range file.Vectors {
		shortID, longID, err := CandidateSessionIDs(vector.Harness, vector.Key)
		require.NoError(t, err)
		require.Equal(t, vector.SessionID, shortID)
		require.Equal(t, vector.LongSessionID, longID)
		require.True(t, sessionIDPattern.MatchString(shortID) && sessionIDPattern.MatchString(longID))
	}
	for _, invalid := range file.InvalidKeys {
		_, _, err := CandidateSessionIDs("openai-sdk", invalid.Key)
		require.Error(t, err, invalid.Key)
	}
	for _, collision := range file.Collisions {
		for index, key := range collision.Keys {
			shortID, longID, err := CandidateSessionIDs(collision.Harness, key)
			require.NoError(t, err)
			require.Equal(t, collision.SessionID, shortID)
			require.Equal(t, collision.LongSessionIDs[index], longID)
		}
	}
	for _, testCase := range file.StartSessionCases {
		if testCase.Expected.SessionID != nil {
			require.True(t, IsSessionIDOfKey(testCase.Harness, testCase.Key, *testCase.Expected.SessionID), testCase.Name)
		}
	}
	key, err := NewIdempotencyKey()
	require.NoError(t, err)
	_, err = IdempotencyKeyTime(key)
	require.NoError(t, err, "dexcli makes valid keys")
}

// Every open_url.json URL is opened or refused as written.
func TestOpenURLConformanceCases(t *testing.T) {
	var file struct {
		Cases []struct {
			Description string  `json:"description"`
			WebURL      *string `json:"web_url"`
			URL         string  `json:"url"`
			IsAllowed   bool    `json:"is_allowed"`
		} `json:"cases"`
	}
	readConformanceFile(t, "open_url.json", &file)
	require.NotEmpty(t, file.Cases)
	for _, testCase := range file.Cases {
		webURL := ""
		if testCase.WebURL != nil {
			webURL = *testCase.WebURL
		}
		require.Equal(t, testCase.IsAllowed, IsOpenURLAllowed(webURL, testCase.URL), testCase.Description)
	}
}

// dexcli finds the missing auth variable of each environment.json case, and
// the launcher's allow-list yields the expected environment.
func TestEnvironmentConformanceCases(t *testing.T) {
	var file struct {
		TerminalVariables []string `json:"terminal_variables"`
		Cases             []struct {
			Name                  string            `json:"name"`
			Auth                  DexAuth           `json:"auth"`
			LauncherEnvironment   map[string]string `json:"launcher_environment"`
			TerminalUIEnvironment map[string]string `json:"terminal_ui_environment"`
			MissingAuthVariable   *string           `json:"missing_auth_variable"`
		} `json:"cases"`
	}
	readConformanceFile(t, "environment.json", &file)
	require.Equal(t, file.TerminalVariables, TerminalVariables)
	for _, testCase := range file.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			expectedMissing := ""
			if testCase.MissingAuthVariable != nil {
				expectedMissing = *testCase.MissingAuthVariable
			}
			require.Equal(t, expectedMissing, MissingAuthVariable(environmentLookup(testCase.TerminalUIEnvironment), testCase.Auth))
			allowed := append([]string(nil), TerminalVariables...)
			if variable := RequiredAuthVariable(testCase.Auth); variable != "" {
				allowed = append(allowed, variable)
			}
			built := map[string]string{}
			for _, name := range allowed {
				if value, isSet := testCase.LauncherEnvironment[name]; isSet {
					built[name] = value
				}
			}
			require.Equal(t, testCase.TerminalUIEnvironment, built)
		})
	}
}

// idempotency_table.json describes the launcher side's key table, which
// dexcli does not keep. This model of the rule checks that the cases agree
// with the retention and capacity that dexcli's recovery relies on.
func TestIdempotencyTableConformanceCases(t *testing.T) {
	var file struct {
		Limits struct {
			Capacity    int   `json:"capacity"`
			RetentionMS int64 `json:"retention_ms"`
		} `json:"limits"`
		Cases []struct {
			Name   string `json:"name"`
			Events []struct {
				AtMS   int64  `json:"at_ms"`
				Op     string `json:"op"`
				Key    string `json:"key"`
				Prefix string `json:"prefix"`
				Count  int    `json:"count"`
				State  string `json:"state"`
			} `json:"events"`
		} `json:"cases"`
	}
	readConformanceFile(t, "idempotency_table.json", &file)
	require.Equal(t, 256, file.Limits.Capacity)
	require.Equal(t, int64(600_000), file.Limits.RetentionMS)
	for _, testCase := range file.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			table := keyTableModel{capacity: file.Limits.Capacity, retentionMS: file.Limits.RetentionMS, finishedAt: map[string]int64{}}
			for _, event := range testCase.Events {
				switch event.Op {
				case "arrive":
					table.arrive(event.AtMS, event.Key)
				case "finish":
					table.finish(event.AtMS, event.Key)
				case "fill":
					for index := range event.Count {
						key := event.Prefix + strconv.Itoa(index)
						table.arrive(event.AtMS+int64(index), key)
						table.finish(event.AtMS+int64(index), key)
					}
				case "query":
					require.Equal(t, event.State, table.state(event.AtMS, event.Key), event.Key)
				default:
					t.Fatalf("unknown op %q", event.Op)
				}
			}
		})
	}
}

// keyTableModel is the contract's key table rule, for its conformance cases.
type keyTableModel struct {
	capacity    int
	retentionMS int64
	finishedAt  map[string]int64
}

const keyRunning = -1

func (table *keyTableModel) arrive(at int64, key string) {
	table.dropExpired(at)
	if _, exists := table.finishedAt[key]; !exists {
		table.finishedAt[key] = keyRunning
	}
	table.dropOverCapacity()
}

func (table *keyTableModel) finish(at int64, key string) {
	table.finishedAt[key] = at
	table.dropOverCapacity()
}

func (table *keyTableModel) state(at int64, key string) string {
	table.dropExpired(at)
	finished, exists := table.finishedAt[key]
	switch {
	case !exists:
		return "unknown"
	case finished == keyRunning:
		return "running"
	}
	return "done"
}

func (table *keyTableModel) dropExpired(at int64) {
	for key, finished := range table.finishedAt {
		if finished != keyRunning && at-finished >= table.retentionMS {
			delete(table.finishedAt, key)
		}
	}
}

func (table *keyTableModel) dropOverCapacity() {
	type finishedKey struct {
		at  int64
		key string
	}
	var finished []finishedKey
	for key, at := range table.finishedAt {
		if at != keyRunning {
			finished = append(finished, finishedKey{at: at, key: key})
		}
	}
	sort.Slice(finished, func(left, right int) bool {
		if finished[left].at != finished[right].at {
			return finished[left].at < finished[right].at
		}
		return finished[left].key < finished[right].key
	})
	excess := len(table.finishedAt) - table.capacity
	for index := 0; index < excess && index < len(finished); index++ {
		delete(table.finishedAt, finished[index].key)
	}
}
