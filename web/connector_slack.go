// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/superdurable/dex/web/api"
)

type slackChannelView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsPrivate bool   `json:"isPrivate"`
}

type slackUserView struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	ImageURL    string `json:"imageUrl,omitempty"`
}

type slackListResponse[T any] struct {
	OK               bool   `json:"ok"`
	Error            string `json:"error"`
	Channels         []T    `json:"channels"`
	Members          []T    `json:"members"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

func (response *slackListResponse[T]) slackOK() (bool, string) {
	return response.OK, response.Error
}

type slackChannel struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	IsPrivate  bool   `json:"is_private"`
	IsMember   bool   `json:"is_member"`
	IsArchived bool   `json:"is_archived"`
}

type slackUser struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
	IsBot   bool   `json:"is_bot"`
	Profile struct {
		DisplayName string `json:"display_name"`
		RealName    string `json:"real_name"`
		Image48     string `json:"image_48"`
	} `json:"profile"`
}

func (setup *connectorSetup) handleListSlackChannels(response http.ResponseWriter, request *http.Request) {
	connection, ok := setup.authorizeSlackResourceRead(response, request)
	if !ok {
		return
	}
	channels := make([]slackChannelView, 0)
	cursor := ""
	for {
		var result slackListResponse[slackChannel]
		query := url.Values{"exclude_archived": {"true"}, "limit": {"200"}, "types": {"public_channel,private_channel"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		if err := setup.callSlackAPI(request, connection, "conversations.list", query, &result); err != nil {
			api.WriteCodedError(response, http.StatusBadGateway, "SLACK_CHANNELS_UNAVAILABLE", "Slack channels are unavailable")
			return
		}
		for _, channel := range result.Channels {
			if channel.ID == "" || channel.Name == "" || channel.IsArchived || !channel.IsMember {
				continue
			}
			channels = append(channels, slackChannelView{ID: channel.ID, Name: channel.Name, IsPrivate: channel.IsPrivate})
		}
		cursor = strings.TrimSpace(result.ResponseMetadata.NextCursor)
		if cursor == "" {
			break
		}
	}
	writeWebJSON(response, http.StatusOK, map[string]any{"channels": channels})
}

func (setup *connectorSetup) handleListSlackUsers(response http.ResponseWriter, request *http.Request) {
	connection, ok := setup.authorizeSlackResourceRead(response, request)
	if !ok {
		return
	}
	users := make([]slackUserView, 0)
	cursor := ""
	for {
		var result slackListResponse[slackUser]
		query := url.Values{"limit": {"200"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		if err := setup.callSlackAPI(request, connection, "users.list", query, &result); err != nil {
			api.WriteCodedError(response, http.StatusBadGateway, "SLACK_USERS_UNAVAILABLE", "Slack members are unavailable")
			return
		}
		for _, user := range result.Members {
			if user.ID == "" || user.Deleted || user.IsBot || user.ID == "USLACKBOT" {
				continue
			}
			displayName := strings.TrimSpace(user.Profile.DisplayName)
			if displayName == "" {
				displayName = strings.TrimSpace(user.Profile.RealName)
			}
			if displayName == "" {
				displayName = user.Name
			}
			users = append(users, slackUserView{ID: user.ID, DisplayName: displayName, ImageURL: user.Profile.Image48})
		}
		cursor = strings.TrimSpace(result.ResponseMetadata.NextCursor)
		if cursor == "" {
			break
		}
	}
	writeWebJSON(response, http.StatusOK, map[string]any{"users": users})
}

func (setup *connectorSetup) authorizeSlackResourceRead(
	response http.ResponseWriter,
	request *http.Request,
) (localConnectorConnection, bool) {
	_, identity, ok := setup.authorizeConnectionWrite(response, request)
	if !ok {
		return localConnectorConnection{}, false
	}
	if identity.ConnectorID != "slack" {
		api.WriteCodedError(response, http.StatusNotFound, "SLACK_CONNECTION_REQUIRED", "Slack connection is required")
		return localConnectorConnection{}, false
	}
	connection, found, err := setup.store.get(identity.ConnectorID, identity.ConnectionName)
	if err != nil || !found {
		api.WriteCodedError(response, http.StatusConflict, "SLACK_CONNECTION_NOT_READY", "Slack connection is not configured")
		return localConnectorConnection{}, false
	}
	return connection, true
}

func (setup *connectorSetup) callSlackAPI(
	request *http.Request,
	connection localConnectorConnection,
	method string,
	query url.Values,
	destination any,
) error {
	var botToken string
	if err := json.Unmarshal(connection.Credentials["bot_token"], &botToken); err != nil || strings.TrimSpace(botToken) == "" {
		return fmt.Errorf("Slack bot token is unavailable")
	}
	target := strings.TrimRight(setup.slackAPIBaseURL, "/") + "/" + method + "?" + query.Encode()
	providerRequest, err := http.NewRequestWithContext(request.Context(), http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	providerRequest.Header.Set("Authorization", "Bearer "+botToken)
	providerResponse, err := setup.slackHTTPClient.Do(providerRequest)
	if err != nil {
		return err
	}
	defer providerResponse.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(providerResponse.Body, (4<<20)+1))
	if err != nil || len(contents) > 4<<20 || providerResponse.StatusCode != http.StatusOK {
		return fmt.Errorf("Slack API response is invalid")
	}
	if err := json.Unmarshal(contents, destination); err != nil {
		return err
	}
	status, ok := destination.(interface{ slackOK() (bool, string) })
	if !ok {
		return fmt.Errorf("Slack API destination cannot report status")
	}
	isOK, providerError := status.slackOK()
	if !isOK {
		return fmt.Errorf("Slack rejected request: %s", providerError)
	}
	return nil
}
