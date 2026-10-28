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
	"encoding/json"
	"fmt"
	"os"

	"github.com/superdurable/dex-connectors-library/connectors/slack"
	"github.com/superdurable/dex-connectors-library/sdkgo"
)

const (
	ToolPostToSlack = "post_to_slack"

	postToSlackStepType = "PostToSlack"
	slackConnectionName = "ai-agent-slack"
	unusedSlackToken    = "not-used-by-post_to_slack"
)

var postToSlackToolDefinition = ToolDefinition{
	Name:        ToolPostToSlack,
	Description: "Post a message to the team's Slack channel. The user approves every post.",
	Parameters:  objectSchema(map[string]any{"text": stringSchema("The message to post.")}, "text"),
}

// SlackSettings is the Dex Slack connection and channel that post_to_slack writes to.
type SlackSettings struct {
	Connection  slack.Connection
	ChannelID   string
	hasBotToken bool
}

// NewSlackSettingsFromEnvironment reads SLACK_BOT_TOKEN, DEX_AI_AGENT_SLACK_CHANNEL and DEX_AI_AGENT_SLACK_ENDPOINT.
func NewSlackSettingsFromEnvironment() (SlackSettings, error) {
	reference := sdkgo.ConnectionRef{Provider: slack.ConnectorID, Name: slackConnectionName}
	botToken := os.Getenv("SLACK_BOT_TOKEN")
	client, err := slack.New(
		slack.Config{Endpoint: os.Getenv("DEX_AI_AGENT_SLACK_ENDPOINT")},
		sdkgo.StaticCredentialProvider[slack.Credentials]{reference: {
			BotToken: sdkgo.NewSecretString(botToken),
			// Slack connector v0.7.0 validates all three tokens; posting uses only the bot token.
			UserToken: sdkgo.NewSecretString(unusedSlackToken),
			AppToken:  sdkgo.NewSecretString(unusedSlackToken),
		}},
	)
	if err != nil {
		return SlackSettings{}, fmt.Errorf("create the Slack connector client: %w", err)
	}
	connection, err := slack.NewConnection(client, reference)
	if err != nil {
		return SlackSettings{}, fmt.Errorf("create the Slack connection: %w", err)
	}
	return SlackSettings{Connection: connection, ChannelID: os.Getenv("DEX_AI_AGENT_SLACK_CHANNEL"), hasBotToken: botToken != ""}, nil
}

// IsConfigured reports whether the model is offered post_to_slack.
func (settings SlackSettings) IsConfigured() bool {
	return settings.hasBotToken && settings.ChannelID != ""
}

// mapToolCallToSlackMessage leaves the text empty for bad arguments; the connector then takes its Defect branch.
func (settings SlackSettings) mapToolCallToSlackMessage(call ToolCall) slack.PostChannelMessageInput {
	var arguments struct {
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(call.Arguments), &arguments) != nil {
		arguments.Text = ""
	}
	return slack.PostChannelMessageInput{ChannelID: settings.ChannelID, Text: arguments.Text}
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
	return OutcomeFailed, fmt.Sprintf(
		"The Slack connection cannot post (%s). Set SLACK_BOT_TOKEN and DEX_AI_AGENT_SLACK_CHANNEL.", detail)
}
