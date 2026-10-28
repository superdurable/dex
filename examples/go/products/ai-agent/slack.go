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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/superdurable/dex-connectors-library/connectors/slack"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex-connectors-library/sdkgo/projectconfig"
	"github.com/superdurable/dex-connectors-library/sdkgo/projectconfig/provider"
)

const (
	ToolPostToSlack = "post_to_slack"

	aiAgentFlowType               = "AIAgentFlow"
	postToSlackStepType           = "PostToSlack"
	postChannelMessageOperationID = "postChannelMessage"
	slackConnectionName           = "ai-agent-slack"
)

var postToSlackToolDefinition = ToolDefinition{
	Name:        ToolPostToSlack,
	Description: "Post a message to the team's Slack channel. The user approves every post.",
	Parameters:  objectSchema(map[string]any{"text": stringSchema("The message to post.")}, "text"),
}

// SlackChannelSetting is the PostToSlack setting that the channel picker in Dex Web saves.
type SlackChannelSetting struct {
	ChannelID string `json:"channelId"`
}

// SlackSettings is the Slack connection and channel that Dex Web set up for post_to_slack.
type SlackSettings struct {
	Connection slack.Connection
	Channel    sdkgo.ConnectorLoadedConfiguration[SlackChannelSetting]
	// NotSetUpReason is empty once Dex Web holds both the connection and the channel.
	NotSetUpReason string
}

// SlackChannelSettingRef identifies the channel setting of the PostToSlack Step in Dex Web.
func SlackChannelSettingRef() sdkgo.ConnectorConfigurationRef {
	return sdkgo.ConnectorConfigurationRef{
		ConnectorID: slack.ConnectorID, ConnectionName: slackConnectionName, OperationID: postChannelMessageOperationID,
		FlowType: aiAgentFlowType, StepType: postToSlackStepType,
	}
}

// LoadSlackSettings reads the Slack connection and channel from the Dex Web store in DEX_CONNECTOR_CONFIG_FILE.
// Slack that is not set up yet is not an error: the model is then not offered post_to_slack.
func LoadSlackSettings(ctx context.Context) (SlackSettings, error) {
	if os.Getenv(projectconfig.LocalConnectionsFileEnvironment) == "" && os.Getenv("DEX_PROJECT_ID") == "" {
		return newSlackSettingsNotSetUp(projectconfig.LocalConnectionsFileEnvironment + " is not set")
	}
	project, err := projectconfig.LoadFromEnvironment(ctx)
	if errors.Is(err, os.ErrNotExist) {
		return newSlackSettingsNotSetUp("Dex Web has not saved a connection yet")
	}
	if err != nil {
		return SlackSettings{}, fmt.Errorf("load the connector configuration: %w", err)
	}
	connection, err := slack.NewProjectConnection(project, slackConnectionName)
	if errors.Is(err, projectconfig.ErrObjectNotFound) {
		return newSlackSettingsNotSetUp("Dex Web has no Slack connection named " + slackConnectionName)
	}
	if err != nil {
		return SlackSettings{}, fmt.Errorf("open the Slack connection: %w", err)
	}
	channel, err := provider.LoadOperationConfiguration[SlackChannelSetting](project.Configuration, SlackChannelSettingRef())
	if errors.Is(err, projectconfig.ErrObjectNotFound) || (err == nil && channel.Value.ChannelID == "") {
		return SlackSettings{Connection: connection, NotSetUpReason: "no channel is selected for the PostToSlack Step in Dex Web"}, nil
	}
	if err != nil {
		return SlackSettings{}, fmt.Errorf("load the Slack channel setting: %w", err)
	}
	return SlackSettings{Connection: connection, Channel: channel}, nil
}

// IsConfigured reports whether the model is offered post_to_slack.
func (settings SlackSettings) IsConfigured() bool {
	return settings.NotSetUpReason == ""
}

// mapToolCallToSlackMessage leaves the text empty for bad arguments; the connector then takes its Defect branch.
func (settings SlackSettings) mapToolCallToSlackMessage(call ToolCall) slack.PostChannelMessageInput {
	var arguments struct {
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(call.Arguments), &arguments) != nil {
		arguments.Text = ""
	}
	return slack.PostChannelMessageInput{ChannelID: settings.Channel.Value.ChannelID, Text: arguments.Text}
}

// newSlackSettingsNotSetUp still registers PostToSlack; every post then takes the connector's Defect branch.
func newSlackSettingsNotSetUp(reason string) (SlackSettings, error) {
	client, err := slack.New(slack.Config{}, slackCredentialsNotSetUp{reason: reason})
	if err != nil {
		return SlackSettings{}, fmt.Errorf("create the Slack client: %w", err)
	}
	connection, err := slack.NewConnection(client, sdkgo.ConnectionRef{Provider: slack.ConnectorID, Name: slackConnectionName})
	if err != nil {
		return SlackSettings{}, fmt.Errorf("create the Slack connection: %w", err)
	}
	return SlackSettings{Connection: connection, NotSetUpReason: reason}, nil
}

type slackCredentialsNotSetUp struct {
	reason string
}

func (credentials slackCredentialsNotSetUp) Resolve(sdkgo.Call) (slack.Credentials, error) {
	return slack.Credentials{}, errors.New(credentials.reason)
}

func (credentials slackCredentialsNotSetUp) ResolveWithRefresh(
	context.Context, sdkgo.Call, sdkgo.CredentialRefreshDriver[slack.Credentials],
) (slack.Credentials, error) {
	return slack.Credentials{}, errors.New(credentials.reason)
}

func describeSlackPost(result slack.PostChannelMessageResult) (string, string) {
	detail := "no detail"
	if result.Failure != nil {
		detail = result.Failure.Message
	}
	switch result.Branch {
	case slack.PostChannelMessageBranchSent:
		message := result.Value.Message
		return OutcomeDone, fmt.Sprintf("Posted to Slack channel %s (message %s).", message.ChannelID, message.Timestamp)
	case slack.PostChannelMessageBranchUncertain:
		return OutcomeOutcomeUnknown, fmt.Sprintf(
			"Slack did not confirm the message (%s). It may have been posted, so check the channel before trying again.", detail)
	case slack.PostChannelMessageBranchProviderRejected:
		return OutcomeFailed, fmt.Sprintf("Slack rejected the message: %s.", detail)
	}
	return OutcomeFailed, fmt.Sprintf("The Slack connection cannot post (%s). Set it up on the Dex Web Connectors page.", detail)
}
