// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dev

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/superdurable/dex/cli/internal/payloadcodec"
	"github.com/superdurable/dex/web/api"
)

// localCodecServerRun is the codec server that dexcli dev -codec-key-file runs next to Dex Web.
type localCodecServerRun struct {
	server        *payloadcodec.LocalCodecServer
	settings      api.LocalCodecServerSettings
	discoveryPath string
	// discoveryContent lets Close delete the file only while it is still the one this run wrote.
	discoveryContent []byte
}

// startLocalCodecServer serves cfg.CodecKeyFile on 127.0.0.1 and writes the discovery file for Flow commands.
func startLocalCodecServer(cfg *Config) (*localCodecServerRun, error) {
	codec, err := payloadcodec.NewLocalCodecFromKeyFile(cfg.CodecKeyFile)
	if err != nil {
		return nil, fmt.Errorf("codec key file: %w", err)
	}
	token, err := payloadcodec.NewLocalCodecServerToken()
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(payloadcodec.LocalCodecServerHost, "0"))
	if err != nil {
		return nil, fmt.Errorf("start local codec server: %w", err)
	}
	origins := payloadcodec.LocalWebOrigins(cfg.WebPort)
	if bindIP := net.ParseIP(cfg.BindAddress); bindIP != nil && bindIP.IsLoopback() && cfg.BindAddress != "127.0.0.1" {
		origins = append(origins, "http://"+net.JoinHostPort(cfg.BindAddress, strconv.Itoa(cfg.WebPort)))
	}
	server, err := payloadcodec.NewLocalCodecServer(codec, token, origins, listener)
	if err != nil {
		return nil, errors.Join(err, listener.Close())
	}
	run := &localCodecServerRun{
		server:        server,
		settings:      api.LocalCodecServerSettings{Endpoint: "http://" + listener.Addr().String(), Token: token},
		discoveryPath: payloadcodec.DevCodecServerDiscoveryPath(cfg.StateDirectory, cfg.DexPort),
	}
	// Serve returns only after Close, or when the listener fails; requests then fail visibly in clients.
	go func() { _ = server.Serve(listener) }()
	discovery := payloadcodec.DevCodecServerDiscovery{Endpoint: run.settings.Endpoint, Token: token}
	run.discoveryContent, err = payloadcodec.WriteDevCodecServerDiscovery(run.discoveryPath, discovery)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("write local codec server file: %w", err), server.Close())
	}
	return run, nil
}

// removeStaleCodecServerDiscovery deletes a discovery file that a crashed dexcli dev left for this Dex port.
func removeStaleCodecServerDiscovery(cfg *Config) error {
	err := os.Remove(payloadcodec.DevCodecServerDiscoveryPath(cfg.StateDirectory, cfg.DexPort))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale local codec server file: %w", err)
	}
	return nil
}

// Close stops the server and removes the discovery file, so Flow commands stop using it.
func (run *localCodecServerRun) Close() error {
	removeErr := payloadcodec.RemoveDevCodecServerDiscovery(run.discoveryPath, run.discoveryContent)
	return errors.Join(run.server.Close(), removeErr)
}
