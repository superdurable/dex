// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorsmcp

import (
	gmail "github.com/superdurable/dex-connectors-library/connectors/google/gmail/go"
	youtube "github.com/superdurable/dex-connectors-library/connectors/google/youtube/go"
	hackernews "github.com/superdurable/dex-connectors-library/connectors/hackernews/go"
	reddit "github.com/superdurable/dex-connectors-library/connectors/reddit/go"
	slack "github.com/superdurable/dex-connectors-library/connectors/slack/go"
	spotify "github.com/superdurable/dex-connectors-library/connectors/spotify/go"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex-connectors-library/sdkgo/projectconfig"
)

// compiledConnector is one connector compiled into dexcli: its ID and how a project connection opens.
type compiledConnector struct {
	connectorID    string
	openConnection func(project *projectconfig.LoadedProject, connectionName string) (openedConnection, error)
}

// openedConnection is one project connection with a runner for each of its operations.
type openedConnection struct {
	reference sdkgo.ConnectionRef
	runners   []operationRunner
}

// compiledConnectors lists every connector compiled into dexcli. This is the only list; add a connector here.
func compiledConnectors() []compiledConnector {
	return []compiledConnector{
		{connectorID: slack.ConnectorID, openConnection: openSlackConnection},
		{connectorID: gmail.ConnectorID, openConnection: openGmailConnection},
		{connectorID: hackernews.ConnectorID, openConnection: openHackerNewsConnection},
		{connectorID: youtube.ConnectorID, openConnection: openYouTubeConnection},
		{connectorID: reddit.ConnectorID, openConnection: openRedditConnection},
		{connectorID: spotify.ConnectorID, openConnection: openSpotifyConnection},
	}
}

func openSlackConnection(project *projectconfig.LoadedProject, connectionName string) (openedConnection, error) {
	connection, err := slack.NewProjectConnection(project, connectionName)
	if err != nil {
		return openedConnection{}, err
	}
	operations, reference, err := readGeneratedConnection[slack.Operations](connection, connectionName)
	if err != nil {
		return openedConnection{}, err
	}
	return openedConnection{reference: reference, runners: []operationRunner{
		newQueryRunner(operations.ListThreadMessages()),
		newQueryRunner(operations.GetThreadReply()),
		newMutationRunner(operations.PostChannelMessage()),
		newMutationRunner(operations.PostThreadReply()),
	}}, nil
}

func openGmailConnection(project *projectconfig.LoadedProject, connectionName string) (openedConnection, error) {
	connection, err := gmail.NewProjectConnection(project, connectionName)
	if err != nil {
		return openedConnection{}, err
	}
	operations, reference, err := readGeneratedConnection[gmail.Operations](connection, connectionName)
	if err != nil {
		return openedConnection{}, err
	}
	return openedConnection{reference: reference, runners: []operationRunner{
		newQueryRunner(operations.GetMessage()),
		newMutationRunner(operations.SendMessage()),
		newMutationRunner(operations.ReplyToMessage()),
	}}, nil
}

func openHackerNewsConnection(project *projectconfig.LoadedProject, connectionName string) (openedConnection, error) {
	connection, err := hackernews.NewProjectConnection(project, connectionName)
	if err != nil {
		return openedConnection{}, err
	}
	operations, reference, err := readGeneratedConnection[hackernews.Operations](connection, connectionName)
	if err != nil {
		return openedConnection{}, err
	}
	return openedConnection{reference: reference, runners: []operationRunner{
		newQueryRunner(operations.ListStoryIDs()),
		newQueryRunner(operations.GetItem()),
	}}, nil
}

func openYouTubeConnection(project *projectconfig.LoadedProject, connectionName string) (openedConnection, error) {
	connection, err := youtube.NewProjectConnection(project, connectionName)
	if err != nil {
		return openedConnection{}, err
	}
	operations, reference, err := readGeneratedConnection[youtube.Operations](connection, connectionName)
	if err != nil {
		return openedConnection{}, err
	}
	return openedConnection{reference: reference, runners: []operationRunner{
		newQueryRunner(operations.SearchVideos()),
		newQueryRunner(operations.GetVideo()),
		newQueryRunner(operations.GetChannel()),
		newQueryRunner(operations.ListPlaylistItems()),
		newMutationRunner(operations.AddPlaylistItem()),
	}}, nil
}

func openRedditConnection(project *projectconfig.LoadedProject, connectionName string) (openedConnection, error) {
	connection, err := reddit.NewProjectConnection(project, connectionName)
	if err != nil {
		return openedConnection{}, err
	}
	operations, reference, err := readGeneratedConnection[reddit.Operations](connection, connectionName)
	if err != nil {
		return openedConnection{}, err
	}
	return openedConnection{reference: reference, runners: []operationRunner{
		newQueryRunner(operations.GetPost()),
	}}, nil
}

func openSpotifyConnection(project *projectconfig.LoadedProject, connectionName string) (openedConnection, error) {
	connection, err := spotify.NewProjectConnection(project, connectionName)
	if err != nil {
		return openedConnection{}, err
	}
	operations, reference, err := readGeneratedConnection[spotify.Operations](connection, connectionName)
	if err != nil {
		return openedConnection{}, err
	}
	return openedConnection{reference: reference, runners: []operationRunner{
		newQueryRunner(operations.ListPlaylistTracks()),
	}}, nil
}
