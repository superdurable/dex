// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package agentsterminal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"
	"github.com/superdurable/dex/agents/go/core/builtinloop"
	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	repositoryRoot        = filepath.Join("..", "..", "..")
	contractHostScript    = filepath.Join(repositoryRoot, "agents", "cli", "tests", "terminal_ui_contract_host.py")
	transcriptsDirectory  = filepath.Join(repositoryRoot, "agents", "conformance", "terminal_ui", "transcripts")
	buildDexcliOnce       sync.Once
	builtDexcliPath       string
	errBuildDexcli        error
	contractHostStepLimit = "45"
)

// keystrokes types text once the host's record shows afterStep and, when
// set, the screen shows afterScreen.
type keystrokes struct {
	afterStep   int
	text        string
	afterScreen string
}

// transcriptKeys are the keys each tui transcript needs: the user actions
// that make dexcli send its requests, then the exit. imageA and imageB stand
// for two image files.
var transcriptKeys = map[string][]keystrokes{
	"chat_ready_then_exit":                          {{afterStep: 1, text: "\x04"}},
	"chat_put_image":                                {{afterStep: 1, text: "/image {imageA}\r"}, {afterStep: 3, text: "/image {imageB}\r"}, {afterStep: 5, text: "\x04"}},
	"chat_put_image_cancelled_then_recovered":       {{afterStep: 1, text: "/image {imageA}\r"}, {afterStep: -1, text: "\x04"}},
	"chat_put_image_without_get_request_result":     {{afterStep: 1, text: "/image {imageA}\r"}, {afterStep: -1, text: "\x04"}},
	"chat_get_request_result_running_then_done":     {{afterStep: 1, text: "/new\r"}, {afterStep: -1, text: "\x04"}},
	"chat_get_request_result_timed_out_then_resent": {{afterStep: 1, text: "/new\r"}, {afterStep: -1, text: "\x04"}},
	"chat_start_session_conflict":                   {{afterStep: 1, text: "/new\r"}, {afterStep: -1, text: "\x04"}},
	"chat_start_session_long_id":                    {{afterStep: 1, text: "/new\r"}, {afterStep: -1, text: "\x04"}},
	"chat_launcher_session_switch":                  {{afterStep: 1, text: "/new\r"}},
	"chat_start_session_unknown_then_resent":        {{afterStep: 1, text: "/new\r"}, {afterStep: -1, text: "\x04"}},
	"chat_host_stopped":                             {{afterStep: 1, text: "/image {imageA}\r"}, {afterStep: 2, text: "/new\r"}, {afterStep: -1, text: "x"}},
	"chat_notices_and_worker_states":                {{afterStep: -1, text: "\x04"}},
	"chat_unknown_message_type_ignored":             {{afterStep: 3, text: "/connectors\r"}, {afterStep: -1, text: "\x04"}},
	"chat_launcher_closed":                          nil,
	"launch_auth_missing":                           nil,
	"launch_contract_unsupported":                   nil,
	"launch_not_json":                               nil,
	"launch_too_large":                              nil,
	"chat_cancel_races_completion":                  {{afterStep: 1, text: "/image {imageA}\r"}, {afterStep: -1, text: "\x04"}},
	"chat_terminated":                               nil,
	"chat_worker_stopped":                           {{afterStep: -1, text: "x"}},
	// Esc moves /new to the background; the late result asks first.
	"chat_start_session_background_switch": {
		{afterStep: 1, text: "/new\r"}, {afterStep: 2, text: "\x1b"}, {afterStep: -1, afterScreen: "Switch to it?", text: "y"},
		{afterStep: -1, afterScreen: "type a message; it is sent when the session loads", text: "\x04"},
	},
	"chat_start_session_background_stay": {
		{afterStep: 1, text: "/new\r"}, {afterStep: 2, text: "\x1b"}, {afterStep: -1, afterScreen: "Switch to it?", text: "n"},
		{afterStep: -1, afterScreen: "staying in this session", text: "\x04"},
	},
	// A second /new while one starts sends nothing; exit before the answer.
	"chat_start_session_background_exit": {
		{afterStep: 1, text: "/new\r"}, {afterStep: 2, text: "\x1b"},
		{afterStep: 2, afterScreen: "keeps starting", text: "/new\r"},
		{afterStep: 2, afterScreen: "already starting", text: "\x04"},
	},
	"launch_terminal_failed_beats_dex_setup": nil,
	"launch_dex_setup_failed":                nil,
	// The pickers: refresh the connectors once, or resume the one session.
	"connectors_refresh":         {{afterStep: 1, afterScreen: "hacker-news", text: "r"}, {afterStep: -1, afterScreen: "refreshed", text: "q"}},
	"connectors_mcp_unavailable": {{afterStep: 1, afterScreen: "hacker-news", text: "r"}, {afterStep: -1, afterScreen: "Connectors are off", text: "q"}},
	"sessions_resume_result":     {{afterStep: 1, afterScreen: transcriptResumedSessionID, text: "\r"}},
}

// transcriptResumedSessionID is the session sessions_resume_result picks.
const transcriptResumedSessionID = "openai-sdk-20261110-091500-0f1e2d"

// transcriptsWithSessions need a Dex server with these running sessions of
// the launch document's Flow type.
var transcriptsWithSessions = map[string][]string{
	"sessions_resume_result": {transcriptResumedSessionID},
}

// launchSessionID is the session of every launch document in the transcripts.
const launchSessionID = "openai-sdk-20261111-142233-a1b2c3"

// transcriptScreenChecks check what the screen showed, after the host passed.
var transcriptScreenChecks = map[string]func(t *testing.T, screen string){
	"chat_start_session_background_switch": func(t *testing.T, screen string) {
		require.NotContains(t, screen, "Session "+launchSessionID+" keeps running", "the chat switched to the new session")
		require.Regexp(t, `Session openai-sdk-\d{8}-\d{6}-[0-9a-f]{12} keeps running`, screen)
	},
	"chat_start_session_background_exit": func(t *testing.T, screen string) {
		require.Contains(t, screen, "A new session is already starting.")
		require.Contains(t, screen, "Session "+launchSessionID+" keeps running", "the summary names the session the chat shows")
	},
	"chat_launcher_session_switch": func(t *testing.T, screen string) {
		require.NotContains(t, screen, "keeps running", "the launcher prints how to resume")
	},
	"chat_start_session_background_stay": func(t *testing.T, screen string) {
		require.Contains(t, screen, "Session "+launchSessionID+" keeps running", "the chat stayed")
	},
	"connectors_refresh": func(t *testing.T, screen string) {
		require.Contains(t, screen, "slack/support-slack")
		require.Contains(t, screen, "(no connector)")
		require.Contains(t, screen, "refreshed: 3 connector tools")
	},
	"connectors_mcp_unavailable": func(t *testing.T, screen string) {
		require.Contains(t, screen, "hacker-news", "the rows stay after the failed refresh")
	},
}

// hostOptions change how the host starts dexcli for a transcript.
type hostOptions struct {
	isWithoutTerminal bool
	extraEnvironment  []string
}

var transcriptHostOptions = map[string]hostOptions{
	// stdin is /dev/null, so the terminal check fails before the Dex setup.
	"launch_terminal_failed_beats_dex_setup": {isWithoutTerminal: true, extraEnvironment: []string{"DEX_CREDENTIAL_FILE=/home/user/missing-token"}},
	"launch_dex_setup_failed":                {extraEnvironment: []string{"DEX_CREDENTIAL_FILE=/home/user/missing-token"}},
}

// launcherPauses hold the launcher after a step of the test's copy of a
// transcript, so keys typed then reach dexcli before the next step.
// chat_host_stopped needs /new submitted before the Worker stops, because a
// stopped Worker disables the input.
var launcherPauses = map[string]struct {
	afterStep    int
	milliseconds int
}{
	"chat_host_stopped": {afterStep: 2, milliseconds: 1500},
}

// Every transcript a real dexcli can play passes against the contract host
// as launcher, with dexcli on a pseudo-terminal and keys typed into it.
func TestTranscriptsAgainstTheContractHost(t *testing.T) {
	pythonPath, err := exec.LookPath("python3")
	require.NoError(t, err, "the contract host needs python3")
	dexcli := buildDexcli(t)
	paths, err := filepath.Glob(filepath.Join(transcriptsDirectory, "*.json"))
	require.NoError(t, err)
	// This dexcli's own transcripts: SIGTERM, /new in the background, and
	// launch check precedence.
	ownPaths, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	require.NoError(t, err)
	paths = append(paths, ownPaths...)
	for _, path := range paths {
		var transcript struct {
			Name      string   `json:"name"`
			RealRoles []string `json:"real_roles"`
		}
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &transcript))
		if !slices.Contains(transcript.RealRoles, "tui") {
			continue
		}
		t.Run(transcript.Name, func(t *testing.T) {
			keys, isKnown := transcriptKeys[transcript.Name]
			require.True(t, isKnown, "add the keys of a new transcript to transcriptKeys")
			dexAddress := ""
			if sessionIDs, needsSessions := transcriptsWithSessions[transcript.Name]; needsSessions {
				// The engine reads its paths from the environment, so this
				// subtest runs before the parallel ones.
				dexAddress, _ = startEngineWithSessions(t, dexcli, launchFlowType(t, data), sessionIDs)
			} else {
				t.Parallel()
			}
			playTranscript(t, pythonPath, dexcli, data, keys, dexAddress)
		})
	}
}

// launchFlowType returns the session Flow type of a transcript's launch.
func launchFlowType(t *testing.T, transcriptData []byte) string {
	var transcript struct {
		Steps []struct {
			Message struct {
				Session struct {
					FlowType string `json:"flow_type"`
				} `json:"session"`
			} `json:"message"`
		} `json:"steps"`
	}
	require.NoError(t, json.Unmarshal(transcriptData, &transcript))
	flowType := transcript.Steps[0].Message.Session.FlowType
	require.NotEmpty(t, flowType)
	return flowType
}

// startEngineWithSessions starts a private engine with running sessions
// of flowType and no Worker.
func startEngineWithSessions(t *testing.T, dexcli string, flowType string, sessionIDs []string) (string, *dex.Client) {
	serverZD := os.Getenv(agentstest.DexServerZDEnvironmentVariable)
	if serverZD == "" {
		serverZD, _ = exec.LookPath("dex-server-zd")
	}
	if serverZD == "" {
		t.Skip("needs dex-server-zd (DEX_SERVER_ZD_BINARY)")
	}
	t.Setenv(agentstest.DexcliEnvironmentVariable, dexcli)
	t.Setenv(agentstest.DexServerZDEnvironmentVariable, serverZD)
	stateDirectory := t.TempDir()
	engine, err := agentstest.NewPrivateDexEngine(filepath.Join(stateDirectory, "engine"))
	require.NoError(t, err)
	require.NoError(t, engine.Start(context.Background()))
	t.Cleanup(engine.Kill)
	starter, client := newSessionStarter(t, engine.Address(), flowType, stateDirectory)
	for _, sessionID := range sessionIDs {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := starter.StartSession(ctx, sessionID, nil, dex.StartFlowOptions{})
		cancel()
		require.NoError(t, err)
	}
	return engine.Address(), client
}

// newSessionStarter starts sessions of flowType through a typing-only Flow:
// no Worker runs them, so they stay running.
func newSessionStarter(t *testing.T, address string, flowType string, stateDirectory string) (*dexagents.AgentSessionClient, *dex.Client) {
	definition, err := builtinloop.NewAgentDefinition("typing-only", "unused",
		agentstest.NewScriptedModelClient(func(builtinloop.ModelRequest) (agentstest.ScriptedModelReply, error) {
			return agentstest.ScriptedModelReply{}, nil
		}))
	require.NoError(t, err)
	flow, err := builtinloop.NewBuiltinAgentSessionFlow(definition, flowType)
	require.NoError(t, err)
	registry, err := dex.NewRegistry([]dex.Flow{flow})
	require.NoError(t, err)
	cache, err := agentstest.OpenTestBlobCache(filepath.Join(stateDirectory, "starter-blob-cache"))
	require.NoError(t, err)
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: address})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return dexagents.NewAgentSessionClient(client, flow), client
}

func buildDexcli(t *testing.T) string {
	t.Helper()
	buildDexcliOnce.Do(func() {
		directory, err := os.MkdirTemp("", "dexcli-agents-tui-test-")
		if err != nil {
			errBuildDexcli = err
			return
		}
		builtDexcliPath = filepath.Join(directory, "dexcli")
		// The race detector reports to a file, because dexcli sends stderr away.
		command := exec.Command("go", "build", "-race", "-o", builtDexcliPath, "../../cmd/dexcli")
		command.Env = append(os.Environ(), "GOWORK=off")
		if output, err := command.CombinedOutput(); err != nil {
			errBuildDexcli = fmt.Errorf("build dexcli: %w\n%s", err, output)
		}
	})
	require.NoError(t, errBuildDexcli)
	return builtDexcliPath
}

// playTranscript runs the host as launcher against dexcli with the
// transcript's Dex addresses moved to ports where nothing listens, or the
// server address to dexAddress when it is set.
func playTranscript(t *testing.T, pythonPath string, dexcli string, data []byte, keys []keystrokes, dexAddress string) {
	stateDirectory := t.TempDir()
	home := filepath.Join(stateDirectory, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	if dexAddress == "" {
		dexAddress = "127.0.0.1:" + closedPort(t)
	}
	rewritten := strings.ReplaceAll(string(data), "127.0.0.1:8801", dexAddress)
	rewritten = strings.ReplaceAll(rewritten, "127.0.0.1:8802", "127.0.0.1:"+closedPort(t))
	require.NotContains(t, rewritten, ":8801")
	require.NotContains(t, rewritten, ":8802")
	transcriptPath := filepath.Join(stateDirectory, "transcript.json")
	require.NoError(t, os.WriteFile(transcriptPath, []byte(rewritten), 0o600))
	images := map[string]string{"{imageA}": filepath.Join(stateDirectory, "a.png"), "{imageB}": filepath.Join(stateDirectory, "b.txt")}
	for _, path := range images {
		require.NoError(t, os.WriteFile(path, []byte("\x89PNG test bytes"), 0o600))
	}
	var transcript struct {
		Name  string           `json:"name"`
		Steps []map[string]any `json:"steps"`
	}
	require.NoError(t, json.Unmarshal(data, &transcript))
	if pause, hasPause := launcherPauses[transcript.Name]; hasPause {
		var document map[string]any
		require.NoError(t, json.Unmarshal([]byte(rewritten), &document))
		steps := document["steps"].([]any)
		pauseStep := map[string]any{"pause_ms": pause.milliseconds, "role": "launcher"}
		document["steps"] = append(append(append([]any{}, steps[:pause.afterStep+1]...), pauseStep), steps[pause.afterStep+1:]...)
		encoded, err := json.Marshal(document)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(transcriptPath, encoded, 0o600))
		// Decode into fresh maps: json reuses the old step maps and keeps their keys.
		transcript.Steps = nil
		require.NoError(t, json.Unmarshal(encoded, &transcript))
	}
	recordPath := filepath.Join(stateDirectory, "record.jsonl")
	screenPath := filepath.Join(stateDirectory, "screen.log")
	racePrefix := filepath.Join(stateDirectory, "race")
	options := transcriptHostOptions[transcript.Name]
	arguments := []string{contractHostScript, "launcher", "--transcript", transcriptPath, "--record", recordPath,
		"--step-timeout", contractHostStepLimit, raceEnvironmentArgument(racePrefix)}
	if !options.isWithoutTerminal {
		arguments = append(arguments, "--pty", "100x30", "--screen-log", screenPath, "--pty-input-from-stdin")
	}
	for _, assignment := range options.extraEnvironment {
		arguments = append(arguments, "--extra-env="+assignment)
	}
	command := exec.Command(pythonPath, append(arguments, "--", dexcli, "agents-tui")...)
	command.Env = []string{"HOME=" + home, "TERM=xterm-256color", "LANG=en_US.UTF-8", "PATH=" + os.Getenv("PATH"),
		"DEX_AGENTS_TUI_DEBUG=" + os.Getenv("DEX_AGENTS_TUI_DEBUG")}
	stdin, err := command.StdinPipe()
	require.NoError(t, err)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	require.NoError(t, command.Start())
	typed := make(chan error, 1)
	go func() {
		typed <- typeKeys(stdin, recordPath, screenPath, transcript.Steps, keys, images)
	}()
	waitErr := command.Wait()
	if closeErr := stdin.Close(); closeErr != nil && !strings.Contains(closeErr.Error(), "already closed") {
		t.Logf("close the host stdin: %v", closeErr)
	}
	if waitErr != nil {
		requireNoRaceReports(t, racePrefix)
		record, _ := os.ReadFile(recordPath)
		screen, _ := os.ReadFile(screenPath)
		t.Fatalf("the contract host failed: %v\nstderr: %s\nrecord:\n%s\nscreen (last 3000 bytes):\n%q",
			waitErr, stderr.String(), record, tail(string(screen), 3000))
	}
	requireNoRaceReports(t, racePrefix)
	select {
	case err := <-typed:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the keys were not all typed")
	}
	if check, hasCheck := transcriptScreenChecks[transcript.Name]; hasCheck {
		check(t, screenText(screenPath))
	}
}

// typeKeys writes each keystroke once the host's record shows the step it
// follows. Step -1 is the last step.
func typeKeys(stdin io.Writer, recordPath string, screenPath string, steps []map[string]any, keys []keystrokes, images map[string]string) error {
	for _, keystroke := range keys {
		step := keystroke.afterStep
		if step < 0 {
			step = len(steps) - 1
		}
		wantedLines := 1
		for index := 0; index <= step; index++ {
			if _, isMessage := steps[index]["message"]; isMessage {
				wantedLines++
			} else if _, isRaw := steps[index]["raw_line"]; isRaw {
				wantedLines++
			}
		}
		deadline := time.Now().Add(2 * time.Minute)
		for countLines(recordPath) < wantedLines || !strings.Contains(screenText(screenPath), keystroke.afterScreen) {
			if time.Now().After(deadline) {
				return fmt.Errorf("step %d or the screen text %q never happened", step, keystroke.afterScreen)
			}
			time.Sleep(20 * time.Millisecond)
		}
		// The screen handles the message of the step before the keys arrive.
		time.Sleep(150 * time.Millisecond)
		text := keystroke.text
		for placeholder, path := range images {
			text = strings.ReplaceAll(text, placeholder, path)
		}
		if _, err := io.WriteString(stdin, text); err != nil {
			return fmt.Errorf("type %q: %w", keystroke.text, err)
		}
	}
	return nil
}

// screenText returns the PTY output without terminal escape sequences.
func screenText(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return terminalEscapes.ReplaceAllString(string(data), "")
}

// raceEnvironmentArgument gives dexcli the race detector's log path prefix.
func raceEnvironmentArgument(racePrefix string) string {
	return "--extra-env=GORACE=log_path=" + racePrefix
}

// requireNoRaceReports fails the test when a race-built dexcli reported a race.
func requireNoRaceReports(t *testing.T, racePrefix string) {
	t.Helper()
	reports, err := filepath.Glob(racePrefix + "*")
	require.NoError(t, err)
	for _, report := range reports {
		text, _ := os.ReadFile(report)
		t.Errorf("dexcli reported a data race:\n%s", text)
	}
}

func countLines(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return bytes.Count(data, []byte("\n"))
}

// closedPort returns a port that nothing listens on.
func closedPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	return fmt.Sprint(port)
}

func tail(text string, size int) string {
	if len(text) <= size {
		return text
	}
	return text[len(text)-size:]
}
