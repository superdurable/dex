// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build eino_v010

// ci.sh copies this file in for the Eino v0.10 alpha pass; v0.9 lacks these packages.

package dexeino_test

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/automemory"
	"github.com/cloudwego/eino/adk/middlewares/automemory/dream"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	dexeino "github.com/superdurable/dex/agents/go/eino"
)

// The Eino v0.10 memory middlewares make their own model calls inside RunModel, so construction refuses them.
func TestConstructionRefusesTheMemoryMiddlewares(t *testing.T) {
	memory, err := automemory.New[*schema.Message](context.Background(), &automemory.Config[*schema.Message]{
		MemoryDirectory: t.TempDir(), MemoryBackend: automemory.NewInMemoryBackend(), Model: echoModel()})
	require.NoError(t, err)
	dreaming, err := dream.New[*schema.Message](context.Background(), &dream.Config[*schema.Message]{
		MemoryDirectory: t.TempDir(), MemoryBackend: automemory.NewInMemoryBackend(), Model: echoModel()})
	require.NoError(t, err)
	for name, handler := range map[string]adk.ChatModelAgentMiddleware{"automemory": memory, "dream": dreaming} {
		t.Run(name, func(t *testing.T) {
			_, err := dexeino.NewRunner(&adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: echoModel(),
				Handlers: []adk.ChatModelAgentMiddleware{handler}})
			require.ErrorContains(t, err, "makes its own model calls")
		})
	}
}
