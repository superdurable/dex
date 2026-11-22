// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package connectortools offers Dex Connector operations to an agent as tools.
//
// Name each reviewed operation an agent may use with NewConnectorTool, which
// takes the operation's sdkgo.AgentTool, and put the tools in a
// ConnectorToolSource. Pass the source to builtinloop.WithToolSources, or to
// a framework adapter that accepts dexagents.ToolSource values. The tool
// name, description, argument schema, effect, and approval default come from
// the operation's reviewed agent metadata, which connectorctl generates from
// the connector's manifest. No Connector is exposed by default.
//
//	slackConnection, err := slack.NewProjectConnection(project, "support-slack")
//	if err != nil {
//		return err
//	}
//	agentTools, err := slackConnection.AgentTools()
//	if err != nil {
//		return err
//	}
//	tools := []*connectortools.ConnectorTool{}
//	for _, agentTool := range agentTools {
//		if agentTool.Metadata().ToolName != "slack_post_thread_reply" {
//			continue
//		}
//		tool, err := connectortools.NewConnectorTool(agentTool)
//		if err != nil {
//			return err
//		}
//		tools = append(tools, tool)
//	}
//	source, err := connectortools.NewConnectorToolSource(tools)
//	if err != nil {
//		return err
//	}
//	definition, err := builtinloop.NewAgentDefinition("support", "You answer support threads.", model,
//		builtinloop.WithToolSources(source))
//
// Each call runs one operation invocation in its own tool Step, under the
// connector's Call ID, idempotency key, credential handling, and
// single-dispatch fence. The behavior is the Python core's
// dex_agents.connectors and the TypeScript core's
// @superdurable/dex-agents/connectors, checked by the shared conformance file
// agents/conformance/connector_tools.json.
//
// Since 0.6.16.
package connectortools
