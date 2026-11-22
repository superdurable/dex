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

package ordersupport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/superdurable/dex-connectors-library/connectors/slack/go"
	"github.com/superdurable/dex-connectors-library/sdkgo/projectconfig"
	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/connectortools"
)

const (
	// SlackConnectionName is the Slack connection that the project configuration must hold.
	SlackConnectionName = "support-slack"
	// SlackPostThreadReplyToolName is the tool of the Slack Connector's postThreadReply operation.
	SlackPostThreadReplyToolName = "slack_post_thread_reply"

	hostedProjectEnvironmentPrefix = "DEX_PROJECT_"
)

// NewSlackToolSources returns the Slack tool source, or no source when connection is nil.
//
// The source offers one tool, slack_post_thread_reply. Each call runs in its
// own tool Step with the Connector's Call ID, after a person approves it. It
// returns an error when the Connector's agent tools cannot be built.
func NewSlackToolSources(connection *slack.Connection) ([]dexagents.ToolSource, error) {
	if connection == nil {
		return nil, nil
	}
	agentTools, err := connection.AgentTools()
	if err != nil {
		return nil, err
	}
	tools := []*connectortools.ConnectorTool{}
	for _, agentTool := range agentTools {
		if agentTool.Metadata().ToolName != SlackPostThreadReplyToolName {
			continue
		}
		// Every post waits for a person, whatever the manifest recommends.
		tool, err := connectortools.NewConnectorTool(agentTool, connectortools.WithRequiresApproval(true))
		if err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}
	source, err := connectortools.NewConnectorToolSource(tools)
	if err != nil {
		return nil, err
	}
	return []dexagents.ToolSource{source}, nil
}

// OpenSlackProjectConnection opens the Slack connection from the project configuration, or returns nil without one.
//
// The Worker holds no Slack token: the Connector resolves the connection's
// credentials for each call. It returns an error when a requested project
// configuration is incomplete or invalid, so the Worker fails at startup.
func OpenSlackProjectConnection(ctx context.Context) (*slack.Connection, error) {
	if !isProjectConfigurationRequested() {
		logSlackNotSetUp(projectconfig.LocalConnectionsFileEnvironment + " is not set")
		return nil, nil
	}
	project, err := projectconfig.LoadFromEnvironment(ctx)
	if errors.Is(err, os.ErrNotExist) {
		logSlackNotSetUp("the connector configuration file does not exist")
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load the connector configuration: %w", err)
	}
	connection, err := slack.NewProjectConnection(project, SlackConnectionName)
	if errors.Is(err, projectconfig.ErrObjectNotFound) {
		logSlackNotSetUp("the project has no Slack connection named " + SlackConnectionName)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open the Slack connection: %w", err)
	}
	return &connection, nil
}

func isProjectConfigurationRequested() bool {
	if os.Getenv(projectconfig.LocalConnectionsFileEnvironment) != "" {
		return true
	}
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, hostedProjectEnvironmentPrefix) && value != "" {
			return true
		}
	}
	return false
}

func logSlackNotSetUp(reason string) {
	slog.Warn("Slack connector tools are off: " + reason)
}
