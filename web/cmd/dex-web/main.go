// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/web"
	"github.com/superdurable/dex/web/assets"
)

var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := runDexWeb(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runDexWeb(ctx context.Context, arguments []string, output io.Writer, errorOutput io.Writer) (runErr error) {
	cfg, err := parseCommandConfig(arguments, errorOutput)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if cfg.shouldPrintVersion {
		_, err := fmt.Fprintf(output, "dex-web %s\n", version)
		return err
	}
	connection, err := newFlowServiceConnection(cfg)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, connection.Close()) }()
	server, err := web.NewServer(&cfg.web, dexpb.NewFlowServiceClient(connection), assets.Files)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(cfg.web.BindAddress, strconv.Itoa(cfg.web.Port)))
	if err != nil {
		return err
	}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(listener) }()
	select {
	case err := <-serverErrors:
		return err
	case <-ctx.Done():
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownContext)
	serveErr := <-serverErrors
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(shutdownErr, serveErr)
}
