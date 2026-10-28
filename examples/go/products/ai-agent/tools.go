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
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	ToolFetchPage   = "fetch_page"
	ToolPublishNote = "publish_note"
	ToolWait        = "wait"
	ToolAskUser     = "ask_user"

	// Outcomes of a tool result message.
	OutcomeDone           = "done"
	OutcomeDeclined       = "declined"
	OutcomeFailed         = "failed"
	OutcomeOutcomeUnknown = "outcome_unknown"

	maxPageBytes       = 1 << 20
	maxPageTextLength  = 4000
	maxWaitSeconds     = 24 * 60 * 60
	defaultStagePause  = 1500 * time.Millisecond
	pageRequestTimeout = 20 * time.Second
)

// ToolDefinition is the JSON Schema description of a tool that the model sees.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// toolPolicy decides how the Flow runs a tool: read-only calls may repeat, writes wait for approval.
type toolPolicy struct {
	isReadOnly    bool
	needsApproval bool
}

var toolPolicies = map[string]toolPolicy{
	ToolFetchPage:   {isReadOnly: true},
	ToolPublishNote: {needsApproval: true},
	ToolWait:        {isReadOnly: true},
	ToolAskUser:     {isReadOnly: true},
}

var toolDefinitions = []ToolDefinition{
	{
		Name:        ToolFetchPage,
		Description: "Download a web page and return its title and text.",
		Parameters:  objectSchema(map[string]any{"url": stringSchema("The http or https URL to read.")}, "url"),
	},
	{
		Name:        ToolPublishNote,
		Description: "Publish a short note. The user approves every note before it is published.",
		Parameters:  objectSchema(map[string]any{"text": stringSchema("The note to publish.")}, "text"),
	},
	{
		Name:        ToolWait,
		Description: "Pause for a number of seconds before continuing.",
		Parameters: objectSchema(map[string]any{
			"seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": maxWaitSeconds, "description": "How long to wait."},
		}, "seconds"),
	},
	{
		Name:        ToolAskUser,
		Description: "Ask the user a question and wait for the answer.",
		Parameters:  objectSchema(map[string]any{"question": stringSchema("The question to ask.")}, "question"),
	},
}

// Toolbox runs the tools that reach outside Dex. The Flow runs wait and ask_user as Steps.
type Toolbox struct {
	// StagePause slows each tool down so a demo can kill the app during a call.
	StagePause time.Duration
	NotesFile  string
	HTTPClient *http.Client
}

// NewToolboxFromEnvironment reads DEX_AI_AGENT_STAGE_PAUSE and DEX_AI_AGENT_NOTES_FILE.
func NewToolboxFromEnvironment() (*Toolbox, error) {
	stagePause := defaultStagePause
	if value := os.Getenv("DEX_AI_AGENT_STAGE_PAUSE"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return nil, fmt.Errorf("parse DEX_AI_AGENT_STAGE_PAUSE: %w", err)
		}
		stagePause = parsed
	}
	return &Toolbox{
		StagePause: stagePause,
		NotesFile:  environmentOr("DEX_AI_AGENT_NOTES_FILE", filepath.Join(os.TempDir(), "dex-ai-agent", "notes.jsonl")),
		HTTPClient: &http.Client{Timeout: pageRequestTimeout},
	}, nil
}

// RunTool runs a fetch_page or publish_note call. reportProgress records a Dex heartbeat.
func (toolbox *Toolbox) RunTool(ctx context.Context, call ToolCall, reportProgress func(string) error) (string, error) {
	switch call.Name {
	case ToolFetchPage:
		var arguments struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &arguments); err != nil {
			return "", fmt.Errorf("decode %s arguments: %w", call.Name, err)
		}
		return toolbox.fetchPage(ctx, arguments.URL, reportProgress)
	case ToolPublishNote:
		var arguments struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &arguments); err != nil {
			return "", fmt.Errorf("decode %s arguments: %w", call.Name, err)
		}
		return toolbox.publishNote(ctx, call.ID, arguments.Text, reportProgress)
	}
	return "", fmt.Errorf("the toolbox has no tool %q", call.Name)
}

func (toolbox *Toolbox) fetchPage(ctx context.Context, url string, reportProgress func(string) error) (string, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Sprintf("Cannot fetch %q: only http and https URLs are supported.", url), nil
	}
	if err := toolbox.pauseAfter(ctx, reportProgress, "Downloading "+url); err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create the request for %s: %w", url, err)
	}
	response, err := toolbox.HTTPClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxPageBytes))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", url, err)
	}
	if response.StatusCode >= http.StatusBadRequest {
		return fmt.Sprintf("%s returned HTTP %d.", url, response.StatusCode), nil
	}
	if err := toolbox.pauseAfter(ctx, reportProgress, "Reading the page"); err != nil {
		return "", err
	}
	title, text := pageTitleAndText(string(body))
	if err := toolbox.pauseAfter(ctx, reportProgress, "Extracting the text"); err != nil {
		return "", err
	}
	return fmt.Sprintf("Title: %s\n%s", title, truncate(text, maxPageTextLength)), nil
}

// publishNote uses the call ID as its idempotency key, so a repeated call publishes once.
func (toolbox *Toolbox) publishNote(ctx context.Context, callID, text string, reportProgress func(string) error) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "The note is empty, so nothing was published.", nil
	}
	if err := toolbox.pauseAfter(ctx, reportProgress, "Preparing the note"); err != nil {
		return "", err
	}
	isPublished, err := toolbox.hasNote(callID)
	if err != nil {
		return "", err
	}
	if isPublished {
		return "The note was already published by this call.", nil
	}
	if err := toolbox.pauseAfter(ctx, reportProgress, "Publishing the note"); err != nil {
		return "", err
	}
	if err := toolbox.appendNote(PublishedNote{CallID: callID, Text: text, PublishedAt: time.Now().UTC()}); err != nil {
		return "", err
	}
	return "Published the note to " + toolbox.NotesFile + ".", nil
}

// PublishedNote is one line of the notes file.
type PublishedNote struct {
	CallID      string    `json:"callId"`
	Text        string    `json:"text"`
	PublishedAt time.Time `json:"publishedAt"`
}

// ReadPublishedNotes returns every note in the notes file, oldest first.
func (toolbox *Toolbox) ReadPublishedNotes() ([]PublishedNote, error) {
	file, err := os.Open(toolbox.NotesFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open the notes file: %w", err)
	}
	defer file.Close()
	var notes []PublishedNote
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var note PublishedNote
		if err := json.Unmarshal(scanner.Bytes(), &note); err != nil {
			return nil, fmt.Errorf("decode a note: %w", err)
		}
		notes = append(notes, note)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read the notes file: %w", err)
	}
	return notes, nil
}

func (toolbox *Toolbox) hasNote(callID string) (bool, error) {
	notes, err := toolbox.ReadPublishedNotes()
	if err != nil {
		return false, err
	}
	for _, note := range notes {
		if note.CallID == callID {
			return true, nil
		}
	}
	return false, nil
}

func (toolbox *Toolbox) appendNote(note PublishedNote) error {
	if err := os.MkdirAll(filepath.Dir(toolbox.NotesFile), 0o755); err != nil {
		return fmt.Errorf("create the notes directory: %w", err)
	}
	encoded, err := json.Marshal(note)
	if err != nil {
		return fmt.Errorf("encode the note: %w", err)
	}
	file, err := os.OpenFile(toolbox.NotesFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open the notes file: %w", err)
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return errors.Join(fmt.Errorf("write the note: %w", err), file.Close())
	}
	return file.Close()
}

func (toolbox *Toolbox) pauseAfter(ctx context.Context, reportProgress func(string) error, progress string) error {
	if err := reportProgress(progress); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(toolbox.StagePause):
		return nil
	}
}

var (
	pageTitlePattern      = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	pageNonTextPattern    = regexp.MustCompile(`(?is)<(script|style|head)[^>]*>.*?</(script|style|head)>`)
	pageTagPattern        = regexp.MustCompile(`(?s)<[^>]*>`)
	pageWhitespacePattern = regexp.MustCompile(`\s+`)
)

func pageTitleAndText(page string) (string, string) {
	title := "(none)"
	if match := pageTitlePattern.FindStringSubmatch(page); match != nil {
		title = collapseWhitespace(html.UnescapeString(match[1]))
	}
	text := pageNonTextPattern.ReplaceAllString(page, " ")
	text = pageTagPattern.ReplaceAllString(text, " ")
	return title, collapseWhitespace(html.UnescapeString(text))
}

func collapseWhitespace(text string) string {
	return strings.TrimSpace(pageWhitespacePattern.ReplaceAllString(text, " "))
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}
